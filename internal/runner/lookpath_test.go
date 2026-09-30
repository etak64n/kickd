package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeProgram creates an empty program named name in dir: an executable
// file on macOS and Linux, and a file with the extension .exe on Windows.
// It returns the path that lookPath finds.
func makeProgram(t *testing.T, dir, name string) string {
	t.Helper()
	path, mode := filepath.Join(dir, name), os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		path, mode = path+".exe", 0o644
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// pathEnv returns an environment whose PATH lists dirs.
func pathEnv(dirs ...string) []string {
	env := []string{"PATH=" + strings.Join(dirs, string(os.PathListSeparator))}
	if runtime.GOOS == "windows" {
		env = append(env, "PATHEXT=.COM;.EXE;.BAT;.CMD")
	}
	return env
}

func TestLookPathFindsAProgramInTheFirstDirectoryOfPathThatHasIt(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	want := makeProgram(t, first, "deploy")
	makeProgram(t, second, "deploy")
	if got, err := lookPath("deploy", pathEnv(first, second)); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestLookPathLooksPastADirectoryWithoutTheProgram(t *testing.T) {
	root := t.TempDir()
	empty, tools := filepath.Join(root, "empty"), filepath.Join(root, "tools")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	want := makeProgram(t, tools, "deploy")
	if got, err := lookPath("deploy", pathEnv(empty, tools)); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestLookPathSkipsARelativeDirectoryOfPath(t *testing.T) {
	dir := t.TempDir()
	makeProgram(t, filepath.Join(dir, "bin"), "deploy")
	t.Chdir(dir)
	if got, err := lookPath("deploy", pathEnv("bin")); err == nil {
		t.Errorf("lookPath found %q through a relative directory", got)
	}
}

func TestLookPathReportsAProgramThatItDoesNotFind(t *testing.T) {
	if _, err := lookPath("deploy", pathEnv(t.TempDir())); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("lookPath: %v, want exec.ErrNotFound", err)
	}
}

func TestLookPathSkipsADirectoryWithTheNameOfTheProgram(t *testing.T) {
	root := t.TempDir()
	dirs, tools := filepath.Join(root, "dirs"), filepath.Join(root, "tools")
	if err := os.MkdirAll(filepath.Join(dirs, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := os.MkdirAll(filepath.Join(dirs, "deploy.exe"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := makeProgram(t, tools, "deploy")
	if got, err := lookPath("deploy", pathEnv(dirs, tools)); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestEnvValueTakesTheLastEntryOfTheName(t *testing.T) {
	if got := envValue([]string{"PATH=/usr/bin", "HOME=/root", "PATH=/opt/bin"}, "PATH"); got != "/opt/bin" {
		t.Errorf("envValue = %q, want /opt/bin", got)
	}
}
