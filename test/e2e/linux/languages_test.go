//go:build e2e && linux

package linux

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// languages lists the events of testdata/languages and testdata/languages-ja
// with the program that each one needs.
var languages = []struct {
	event   string
	program string
}{
	{"shell-string", "sh"},
	{"sh", "sh"},
	{"python", "python3"},
	{"node", "node"},
	{"ruby", "ruby"},
	{"perl", "perl"},
	{"rust", "rustc"},
	{"pwsh", "pwsh"},
}

// needs skips the test when program is not installed, or fails it when
// KICKD_E2E_ALL=1 says that every program must be there, as in the CI.
func needs(t *testing.T, program string) {
	t.Helper()
	if _, err := exec.LookPath(program); err != nil {
		if os.Getenv("KICKD_E2E_ALL") == "1" {
			t.Fatalf("%s is not installed", program)
		}
		t.Skipf("%s is not installed", program)
	}
}

// buildRust builds the Rust program of the fixture with rustc, as its user
// would, into the name that the config runs.
func buildRust(h *home, dir, source, program string) {
	h.t.Helper()
	build := exec.Command("rustc", "--crate-name", "report", "-o", program, source)
	build.Dir = h.path(dir)
	if out, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("rustc: %v\n%s", err, out)
	}
}

// checkRun checks that the program of the run printed the values of its
// run, and that kickd recorded both of its output streams.
func checkRun(t *testing.T, r run, event, msg, dir string) {
	t.Helper()
	if r.Status != "succeeded" || r.line("event") != event || r.line("trigger") != "manual" || r.line("msg") != msg {
		t.Errorf("%s printed:\n%s", r.Status, r.Output)
	}
	if got, _ := filepath.EvalSymlinks(r.line("dir")); got != dir {
		t.Errorf("ran in %q, want %q", r.line("dir"), dir)
	}
	var payload struct {
		Event string
		RunID int64 `json:"runId"`
		Data  map[string]string
	}
	if err := json.Unmarshal([]byte(r.line("payload")), &payload); err != nil || payload.Event != event || payload.RunID != r.ID || payload.Data["msg"] != msg {
		t.Errorf("the payload file held %q", r.line("payload"))
	}
	if !strings.Contains(r.Output, "to stderr") {
		t.Errorf("the output lacks the line of standard error:\n%s", r.Output)
	}
}

func TestProgramsInEachLanguageGetTheValuesOfTheirRun(t *testing.T) {
	t.Parallel()
	h := newHome(t, "languages")
	if _, err := exec.LookPath("rustc"); err == nil {
		buildRust(h, "rust", "report.rs", "report")
	}
	h.start()
	for _, l := range languages {
		t.Run(l.event, func(t *testing.T) {
			needs(t, l.program)
			r := h.waitForRun(h.fire(l.event, "msg=hello"))
			checkRun(t, r, l.event, "hello", h.path(l.event))
		})
	}
}

func TestProgramsInEachLanguageFailTheRunWithTheirExitCode(t *testing.T) {
	t.Parallel()
	h := newHome(t, "languages")
	if _, err := exec.LookPath("rustc"); err == nil {
		buildRust(h, "rust", "report.rs", "report")
	}
	h.start()
	for _, l := range languages {
		t.Run(l.event, func(t *testing.T) {
			needs(t, l.program)
			r := h.waitForRun(h.fire(l.event, "msg=failing", "code=3"))
			if r.Status != "failed" || r.ExitCode == nil || *r.ExitCode != 3 {
				t.Errorf("%s, exit code %v", r.Status, r.ExitCode)
			}
		})
	}
}

func TestProgramsInEachLanguageHandleJapaneseAndSpaces(t *testing.T) {
	t.Parallel()
	h := newHome(t, "languages-ja")
	if _, err := exec.LookPath("rustc"); err == nil {
		buildRust(h, "作業 フォルダ/rust", "報告 スクリプト.rs", "報告 スクリプト")
	}
	h.start()
	for _, l := range languages {
		t.Run(l.event, func(t *testing.T) {
			needs(t, l.program)
			r := h.waitForRun(h.fire(l.event, "msg=こんにちは"))
			checkRun(t, r, l.event, "こんにちは", h.path("作業 フォルダ", l.event))
		})
	}
}
