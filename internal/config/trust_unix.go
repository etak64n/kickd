//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// osEntries reads the entries of the filesystem of the machine.
type osEntries struct{}

func (osEntries) lstat(path string) (entry, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return entry{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return entry{}, fmt.Errorf("%s: the owner is unknown", path)
	}
	return entry{mode: fi.Mode(), uid: st.Uid}, nil
}

func (osEntries) readlink(path string) (string, error) { return os.Readlink(path) }

// checkRootOnly reports each file of the config of root that another user
// can change, with the entry on the way to it that allows the change.
func checkRootOnly(paths []string) error {
	var errs []error
	for _, p := range paths {
		if err := rootOnly(osEntries{}, p); err != nil {
			errs = append(errs, fmt.Errorf("%s: kickd runs the commands of this file as root, so only root may be able to change it: %w", p, err))
		}
	}
	if len(errs) > 0 {
		return &ValidationError{Problems: errs}
	}
	return nil
}
