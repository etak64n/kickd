//go:build !windows

package config

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// An agent that runs as root runs the commands of its config as root. Anyone
// who can change a file of that config, a directory on the way to the file,
// or a link on the way, can therefore run commands as root, so the agent
// loads only files that root alone can change.

// entry is what the check needs to know about an entry of the filesystem.
type entry struct {
	mode fs.FileMode
	uid  uint32
}

// entries reads entries of a filesystem without following links.
type entries interface {
	lstat(path string) (entry, error)
	readlink(path string) (string, error)
}

// maxLinks bounds the links that the check follows on the way to one file.
const maxLinks = 40

// rootOnly returns an error unless root alone can change the file at the
// absolute path: the file, every directory on the way to it, and every link
// on the way, which it follows.
func rootOnly(fsys entries, path string) error {
	dir := "/"
	e, err := fsys.lstat(dir)
	if err != nil {
		return err
	}
	if err := ownedByRoot(dir, e, false); err != nil {
		return err
	}
	pending := components(path)
	links := 0
	for len(pending) > 0 {
		name := filepath.Join(dir, pending[0])
		pending = pending[1:]
		e, err := fsys.lstat(name)
		if err != nil {
			return err
		}
		if e.mode&fs.ModeSymlink != 0 {
			// Root alone can change the link, because root alone can change
			// the directory that holds it, which the walk has checked.
			if links++; links > maxLinks {
				return fmt.Errorf("more than %d links on the way to %s", maxLinks, path)
			}
			target, err := fsys.readlink(name)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			pending = append(components(target), pending...)
			dir = "/"
			continue
		}
		if err := ownedByRoot(name, e, len(pending) == 0); err != nil {
			return err
		}
		dir = name
	}
	return nil
}

// ownedByRoot returns an error unless root owns the entry and neither its
// group nor other users can write to it. Every entry but the file must be
// a directory.
func ownedByRoot(name string, e entry, file bool) error {
	switch {
	case !file && !e.mode.IsDir():
		return fmt.Errorf("%s is not a directory", name)
	case e.uid != 0:
		return fmt.Errorf("%s belongs to the user with ID %d; make root its owner with sudo chown root %q", name, e.uid, name)
	case e.mode.Perm()&0o022 != 0:
		return fmt.Errorf("%s can be changed by its group or by other users (mode %04o); remove their write permission with sudo chmod go-w %q", name, e.mode.Perm(), name)
	}
	return nil
}

// components splits an absolute slash-separated path into its names.
func components(path string) []string {
	var out []string
	for _, c := range strings.Split(filepath.Clean(path), "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}
