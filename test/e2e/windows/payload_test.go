//go:build e2e && windows

package windows

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"
)

func TestThePayloadFileIsDeletedWhenTheCommandEnds(t *testing.T) {
	t.Parallel()
	h := newHome(t, "payload")
	h.start()
	r := h.waitForRun(h.fire("report"))
	file := r.line("file")
	if r.Status != "succeeded" || file == "" {
		t.Fatalf("report: %s\n%s", r.Status, r.Output)
	}
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the payload file %s after the run: %v, want no such file", file, err)
	}
}

func TestARerunGetsANewPayloadFileWithTheNextAttempt(t *testing.T) {
	t.Parallel()
	h := newHome(t, "payload")
	h.start()
	h.fire("sync")
	notes := h.path("payloads.txt")
	h.waitFor("the first sync notes its payload file", 30*time.Second, func() bool {
		b, _ := os.ReadFile(notes)
		return strings.HasPrefix(string(b), "1 ")
	})
	h.kill()
	h.start()
	h.waitForRuns("sync", 1)
	b, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	// Each line holds the attempt in the payload file of a run, and the file.
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("payloads.txt has %d lines, want 2:\n%s", len(lines), b)
	}
	first, rerun := strings.Fields(lines[0]), strings.Fields(lines[1])
	if len(first) != 2 || len(rerun) != 2 || first[0] != "1" || rerun[0] != "2" || rerun[1] == first[1] {
		t.Errorf("the payload files of the run and of its rerun:\n%s", b)
	}
}
