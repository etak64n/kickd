//go:build !windows

package queue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDatabaseFilesAreOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kickd.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// The -wal and -shm files exist while the connection is open.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s: permissions %o, want 600", filepath.Base(p), perm)
		}
	}
}

func TestTightenSidecars(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "kickd.db")
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(db+suffix, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(db+suffix, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tightenSidecars(db)
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(db + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s: permissions %o, want 600", suffix, perm)
		}
	}
}
