//go:build e2e && windows

package windows

import (
	"testing"
	"time"
)

func TestStartupFiresWhenKickdStarts(t *testing.T) {
	t.Parallel()
	h := newHome(t, "startup")
	h.start()
	if r := h.waitForRuns("prepare", 1)[0]; r.line("trigger") != "startup" {
		t.Errorf("prepare:\n%s", r.Output)
	}
}

func TestStartupDoesNotFireWhenTheConfigIsSaved(t *testing.T) {
	t.Parallel()
	h := newHome(t, "startup")
	h.start()
	h.waitForRuns("prepare", 1)
	offset := fileSize(h.path("kickd.log"))
	h.save("edits/kickd.edited.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	time.Sleep(2 * time.Second)
	if rs := h.runs("prepare"); len(rs) != 1 {
		t.Errorf("prepare ran %d times, want once", len(rs))
	}
}

func TestStartupFiresAgainWhenKickdStartsAgain(t *testing.T) {
	t.Parallel()
	h := newHome(t, "startup")
	h.start()
	h.waitForRuns("prepare", 1)
	h.kill()
	h.start()
	h.waitForRuns("prepare", 2)
}
