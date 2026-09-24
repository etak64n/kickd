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
func TestExamplesLoad(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("found %d example configs, want at least 10", len(files))
	}
	for _, f := range files {
		checkLoads(t, filepath.Base(f), f)
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
