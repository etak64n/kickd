//go:build !windows

package runner

import "os"

// findExecutable reports whether path is an executable file.
func findExecutable(path string, _ []string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", false
	}
	return path, true
}
