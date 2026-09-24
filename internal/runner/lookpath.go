package runner

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// lookPath finds the program name in the directories of the PATH in env,
// the environment that the command gets, so that the env of an event
// decides where its programs are found. Like exec.LookPath, it never takes
// a program from the current directory through a relative entry of PATH.
func lookPath(name string, env []string) (string, error) {
	for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, ok := findExecutable(filepath.Join(dir, name), env); ok {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// envValue returns the value of the last entry for key in env. Windows
// compares the names of environment variables without case.
func envValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && (k == key || runtime.GOOS == "windows" && strings.EqualFold(k, key)) {
			value = v
		}
	}
	return value
}
