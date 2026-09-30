//go:build e2e && windows

package windows

import (
	"strings"
	"testing"
)

func TestSavingTheConfigAddsAnEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	h.save("kickd.edited.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	if r := h.waitForRun(h.fire("test")); r.Status != "succeeded" || strings.TrimSpace(r.Output) != "tested" {
		t.Errorf("test: %s\n%s", r.Status, r.Output)
	}
}

func TestSavingABrokenConfigKeepsThePreviousConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "File watch started", 1)
	offset := fileSize(h.path("kickd.log"))
	h.save("kickd.broken.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reload failed", 1)
	h.write("src/main.c", "int main(void) { return 1; }\n")
	if r := h.waitForRuns("build", 1)[0]; r.Trigger != "file" || strings.TrimSpace(r.Output) != "built" {
		t.Errorf("build: %s by %s\n%s", r.Status, r.Trigger, r.Output)
	}
}
