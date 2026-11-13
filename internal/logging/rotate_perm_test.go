//go:build !windows

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingFileCreatesTheFileWithItsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.log")
	rf, err := openRotating(path, 0, 1, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	rf.Close()
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", st.Mode().Perm(), err)
	}
}

// An existing file loses the permissions that its writer does not give,
// and keeps narrower ones.
func TestRotatingFileNarrowsTheMoreOpenPermissionsOfAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name       string
		have, perm os.FileMode
		want       os.FileMode
	}{
		{"wider", 0o644, 0o600, 0o600},
		{"narrower", 0o600, 0o644, 0o600},
	} {
		path := filepath.Join(dir, c.name+".log")
		if err := os.WriteFile(path, []byte("line\n"), c.have); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, c.have); err != nil {
			t.Fatal(err)
		}
		rf, err := openRotating(path, 0, 1, c.perm, nil)
		if err != nil {
			t.Fatal(err)
		}
		rf.Close()
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != c.want {
			t.Errorf("%s: mode = %v, want %v (%v)", c.name, st.Mode().Perm(), c.want, err)
		}
	}
}
