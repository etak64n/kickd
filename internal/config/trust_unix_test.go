//go:build !windows

package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// fakeEntries is a filesystem of entries that a test lays out: directories,
// files and links, each with its mode and its owner.
type fakeEntries map[string]fakeEntry

type fakeEntry struct {
	entry
	target string // of a link
}

func (f fakeEntries) lstat(path string) (entry, error) {
	e, ok := f[path]
	if !ok {
		return entry{}, fs.ErrNotExist
	}
	return e.entry, nil
}

func (f fakeEntries) readlink(path string) (string, error) {
	e, ok := f[path]
	if !ok || e.mode&fs.ModeSymlink == 0 {
		return "", errors.New("not a link")
	}
	return e.target, nil
}

func fakeDir(uid uint32, perm fs.FileMode) fakeEntry {
	return fakeEntry{entry: entry{fs.ModeDir | perm, uid}}
}
func fakeFile(uid uint32, perm fs.FileMode) fakeEntry { return fakeEntry{entry: entry{perm, uid}} }
func fakeLink(target string) fakeEntry {
	return fakeEntry{entry: entry{fs.ModeSymlink | 0o755, 0}, target: target}
}

// machine is a filesystem like that of a Mac with kickd installed as a
// LaunchDaemon, an events file that links into a repository, and a user.
func machine() fakeEntries {
	return fakeEntries{
		"/":                               fakeDir(0, 0o755),
		"/Library":                        fakeDir(0, 0o755),
		"/Library/Application Support":    fakeDir(0, 0o755),
		"/Library/Application Support/kd": fakeDir(0, 0o755),
		"/Library/Application Support/kd/config.yaml": fakeFile(0, 0o600),
		"/Library/Application Support/kd/app.yaml":    fakeLink("/opt/app/deploy/kickd.yaml"),
		"/Library/Application Support/kd/rel.yaml":    fakeLink("../../../opt/app/deploy/kickd.yaml"),
		"/Library/Application Support/kd/mine.yaml":   fakeLink("/Users/alice/kickd.yaml"),
		"/opt":                       fakeDir(0, 0o755),
		"/opt/app":                   fakeDir(0, 0o755),
		"/opt/app/deploy":            fakeDir(0, 0o755),
		"/opt/app/deploy/kickd.yaml": fakeFile(0, 0o644),
		"/Users":                     fakeDir(0, 0o755),
		"/Users/alice":               fakeDir(501, 0o750),
		"/Users/alice/kickd.yaml":    fakeFile(501, 0o644),
		"/etc":                       fakeLink("private/etc"),
		"/private":                   fakeDir(0, 0o755),
		"/private/etc":               fakeDir(0, 0o755),
		"/private/etc/kd.yaml":       fakeFile(0, 0o644),
		"/tmp":                       fakeDir(0, fs.ModeSticky|0o777),
		"/tmp/kd.yaml":               fakeFile(0, 0o644),
	}
}

func TestRootOnlyAcceptsWhatRootAloneCanChange(t *testing.T) {
	for _, p := range []string{
		"/Library/Application Support/kd/config.yaml",
		"/Library/Application Support/kd/app.yaml", // through an absolute link
		"/Library/Application Support/kd/rel.yaml", // through a relative link
		"/etc/kd.yaml", // through a link on the way
	} {
		if err := rootOnly(machine(), p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestRootOnlyRefusesWhatOtherUsersCanChange(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(fakeEntries)
		path   string
		want   string
	}{
		{"a file that another user owns", nil, "/Library/Application Support/kd/mine.yaml", "/Users/alice belongs to the user with ID 501"},
		{"a file that its group can change", func(f fakeEntries) { f["/opt/app/deploy/kickd.yaml"] = fakeFile(0, 0o664) },
			"/Library/Application Support/kd/app.yaml", "/opt/app/deploy/kickd.yaml can be changed by its group or by other users (mode 0664)"},
		{"a directory on the way that others can change", func(f fakeEntries) { f["/opt/app"] = fakeDir(0, 0o757) },
			"/Library/Application Support/kd/app.yaml", "/opt/app can be changed by its group or by other users"},
		{"the directory of a link that others can change", func(f fakeEntries) { f["/Library/Application Support/kd"] = fakeDir(0, 0o777) },
			"/Library/Application Support/kd/app.yaml", "/Library/Application Support/kd can be changed"},
		{"a directory that anyone can write to", nil, "/tmp/kd.yaml", "/tmp can be changed by its group or by other users (mode 0777)"},
		{"a file in a directory of another user", func(f fakeEntries) { f["/opt/app"] = fakeDir(501, 0o755) },
			"/Library/Application Support/kd/app.yaml", "/opt/app belongs to the user with ID 501"},
		{"a link to itself", func(f fakeEntries) { f["/opt/loop"] = fakeLink("/opt/loop") }, "/opt/loop", "more than 40 links"},
		{"a file where a directory should be", nil, "/opt/app/deploy/kickd.yaml/x", "is not a directory"},
	} {
		fsys := machine()
		if c.change != nil {
			c.change(fsys)
		}
		err := rootOnly(fsys, c.path)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// The error names a command that fixes the entry, with the path quoted for
// the shell.
func TestRootOnlyNamesTheFix(t *testing.T) {
	fsys := machine()
	fsys["/Library/Application Support/kd/config.yaml"] = fakeFile(0, 0o666)
	err := rootOnly(fsys, "/Library/Application Support/kd/config.yaml")
	if want := `sudo chmod go-w "/Library/Application Support/kd/config.yaml"`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %s", err, want)
	}
}
