//go:build e2e && linux

package linux

import (
	"syscall"
	"testing"
)

func TestSavingTheConfigAddsAnEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	h.save("edits/config.edited.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	if r := h.waitForRun(h.fire("test")); r.Status != "succeeded" || r.Output != "tested\n" {
		t.Errorf("test: %s\n%s", r.Status, r.Output)
	}
}

func TestSavingABrokenConfigKeepsThePreviousConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	log := h.path("kickd.log")
	h.waitForLog(log, 0, "File watch started", 1)
	offset := fileSize(log)
	h.save("edits/config.broken.yaml")
	h.waitForLog(log, offset, "Config reload failed", 1)
	// The commands of kickd read the config too, and fail on the broken
	// one, so the log shows that build still runs.
	h.write("src/main.c", "int main(void) { return 1; }\n")
	h.waitForLog(log, offset, "Run completed", 1)
}

func TestSighupReloadsTheConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	if err := h.agent.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
}
