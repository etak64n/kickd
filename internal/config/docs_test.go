package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocumentedConfigsLoad keeps the examples in the documentation valid.
// Every YAML block with an events key must load. Directories that exist
// only on the machine an example was written for are the one accepted
// problem.
func TestDocumentedConfigsLoad(t *testing.T) {
	root := filepath.Join("..", "..")
	files := []string{filepath.Join(root, "README.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?ms)^( *)```yaml\n(.*?)^ *```")
	checked := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// A checkout on Windows can turn line endings into CRLF.
		text := strings.ReplaceAll(string(b), "\r\n", "\n")
		for i, m := range block.FindAllStringSubmatch(text, -1) {
			body := dedent(m[2], len(m[1]))
			if !strings.Contains(body, "events:") {
				continue
			}
			checked++
			path := filepath.Join(t.TempDir(), "kickd.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			checkLoads(t, fmt.Sprintf("%s, YAML block %d", filepath.Base(f), i+1), path)
		}
	}
	if checked < 5 {
		t.Fatalf("found %d config examples in the docs, want at least 5", checked)
	}
}

// dedent removes up to n leading spaces from every line.
func dedent(s string, n int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		trim := 0
		for trim < n && trim < len(l) && l[trim] == ' ' {
			trim++
		}
		lines[i] = l[trim:]
	}
	return strings.Join(lines, "\n")
}

// TestExamplesLoad checks every config file in the examples directory.
// Each example is a whole config file of its own, so each one loads from a
// directory without the others, which would add their events.
func TestExamplesLoad(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("found %d example configs, want at least 10", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		checkLoads(t, filepath.Base(f), path)
	}
}

// checkLoads loads a config and reports every problem except directories
// that exist only on the machine the config was written for.
func checkLoads(t *testing.T, name, path string) {
	t.Helper()
	_, err := Load(path)
	var ve *ValidationError
	if errors.As(err, &ve) {
		for _, p := range ve.Problems {
			if !strings.Contains(p.Error(), "is not a directory") {
				t.Errorf("%s: %v", name, p)
			}
		}
		return
	}
	if err != nil {
		t.Errorf("%s: %v", name, err)
	}
}

// TestReadmeShowsWhatInitWrites keeps the files in the README the same as
// the files that kickd init writes: the config file and the events file,
// in the home directory on macOS and Linux, and in C:\ProgramData on
// Windows.
func TestReadmeShowsWhatInitWrites(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, c := range []struct {
		summary string
		goos    []string
		system  bool
	}{
		{"macOS and Linux", []string{"darwin", "linux"}, false},
		{"Windows", []string{"windows"}, true},
	} {
		_, rest, ok := strings.Cut(readme, "<summary>"+c.summary+"</summary>")
		var blocks []string
		for ok && len(blocks) < 2 {
			_, rest, ok = strings.Cut(rest, "```yaml\n")
			var block string
			if ok {
				block, rest, ok = strings.Cut(rest, "```\n")
			}
			if ok {
				blocks = append(blocks, block)
			}
		}
		if len(blocks) < 2 {
			t.Errorf("the README has no two YAML blocks under %s", c.summary)
			continue
		}
		for _, goos := range c.goos {
			if want := ExampleConfig(goos, c.system); blocks[0] != want {
				t.Errorf("the %s config file in the README differs from what kickd init writes on %s: %s", c.summary, goos, firstDiff(blocks[0], want))
			}
			if want := ExampleEvents(goos); blocks[1] != want {
				t.Errorf("the %s events file in the README differs from what kickd init writes on %s: %s", c.summary, goos, firstDiff(blocks[1], want))
			}
		}
	}
}

// firstDiff describes the first line where got and want differ.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d is %q in the README and %q in kickd init", i+1, gl, wl)
		}
	}
	return "no line differs"
}
