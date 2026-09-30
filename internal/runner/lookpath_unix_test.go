//go:build !windows

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathSkipsAFileThatIsNotExecutable(t *testing.T) {
	root := t.TempDir()
	notes, tools := filepath.Join(root, "notes"), filepath.Join(root, "tools")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "deploy"), []byte("# steps\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := makeProgram(t, tools, "deploy")
	if got, err := lookPath("deploy", pathEnv(notes, tools)); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestEnvValueComparesNamesWithCaseOnMacOSAndLinux(t *testing.T) {
	if got := envValue([]string{"Path=/usr/bin"}, "PATH"); got != "" {
		t.Errorf("envValue = %q, want empty", got)
	}
}
