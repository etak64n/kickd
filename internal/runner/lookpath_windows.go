//go:build windows

package runner

import (
	"os"
	"path/filepath"
	"strings"
)

// findExecutable returns path, or path with one of the extensions of
// PATHEXT in env appended, whichever names a file first, as Windows looks
// programs up.
func findExecutable(path string, env []string) (string, bool) {
	exts := []string{".com", ".exe", ".bat", ".cmd"}
	if v := envValue(env, "PATHEXT"); v != "" {
		exts = exts[:0]
		for _, e := range strings.Split(strings.ToLower(v), ";") {
			if e != "" {
				exts = append(exts, e)
			}
		}
	}
	if filepath.Ext(path) != "" && isFile(path) {
		return path, true
	}
	for _, e := range exts {
		if p := path + e; isFile(p) {
			return p, true
		}
	}
	return "", false
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
