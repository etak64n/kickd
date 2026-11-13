package event

import (
	"fmt"
	"strconv"
	"testing"
)

// A list of paths longer than an environment variable can hold leaves
// KICKD_FILE_PATHS empty, so that the command still starts; the other
// variables and the payload keep their values.
func TestEnvLeavesOutAListOfPathsTooLongForAVariable(t *testing.T) {
	var files []FileChange
	for i := range 2000 {
		files = append(files, FileChange{Path: fmt.Sprintf("/home/user/app/src/components/module-%04d/index.ts", i), Op: "write"})
	}
	v := env(Event{Trigger: KindFile, Files: files})
	if v["KICKD_FILE_PATHS"] != "" {
		t.Errorf("KICKD_FILE_PATHS has %d bytes", len(v["KICKD_FILE_PATHS"]))
	}
	if v["KICKD_FILE_COUNT"] != strconv.Itoa(len(files)) || v["KICKD_FILE_PATH"] != files[len(files)-1].Path {
		t.Errorf("KICKD_FILE_COUNT = %q, KICKD_FILE_PATH = %q", v["KICKD_FILE_COUNT"], v["KICKD_FILE_PATH"])
	}
}

// A list that fits stays whole, up to the limit.
func TestEnvKeepsAListOfPathsThatFits(t *testing.T) {
	var files []FileChange
	for i := 0; ; i++ {
		f := FileChange{Path: fmt.Sprintf("/src/%05d.c", i), Op: "create"}
		if len(joinPaths(append(files, f))) > MaxFilePathsEnv {
			break
		}
		files = append(files, f)
	}
	v := env(Event{Trigger: KindFile, Files: files})
	if got, want := v["KICKD_FILE_PATHS"], joinPaths(files); got != want {
		t.Errorf("KICKD_FILE_PATHS has %d bytes, want %d", len(got), len(want))
	}
}
