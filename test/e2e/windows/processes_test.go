//go:build e2e && windows

package windows

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code that Windows reports for a process that
// runs.
const stillActive = 259

// alive reports whether the process pid runs.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// pidIn waits for the process ID that a script wrote into the file name in
// the home directory. When the test ends, it ends that process and waits
// until it is gone, because Windows cannot remove the home directory while
// a process works in it.
func (h *home) pidIn(name string) int {
	h.t.Helper()
	var pid int
	h.waitFor(name+" is written", 30*time.Second, func() bool {
		b, err := os.ReadFile(h.path(name))
		pid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF")))
		return err == nil && pid > 0
	})
	h.t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
		waitFor(h.t, "the process "+strconv.Itoa(pid)+" ends", 30*time.Second, func() bool { return !alive(pid) }, func() string { return "" })
	})
	return pid
}

func TestABackgroundProcessInAWindowOfItsOwnKeepsRunning(t *testing.T) {
	t.Parallel()
	h := newHome(t, "processes")
	h.start()
	r := h.waitForRun(h.fire("start-server"))
	pid := h.pidIn("server.pid")
	if r.Status != "succeeded" || r.DurationMs > 8000 {
		t.Errorf("start-server: %s after %d ms", r.Status, r.DurationMs)
	}
	if !alive(pid) {
		t.Errorf("the server %d stopped with the script", pid)
	}
}

func TestABackgroundProcessThatHoldsTheOutputFailsTheRunAfter10Seconds(t *testing.T) {
	t.Parallel()
	h := newHome(t, "processes")
	h.start()
	r := h.waitForRun(h.fire("start-server-holding-output"))
	pid := h.pidIn("server.pid")
	if r.Status != "failed" || r.Reason != "wait_failed" || r.DurationMs < 9000 || r.DurationMs > 25000 {
		t.Errorf("start-server-holding-output: %s %s after %d ms", r.Status, r.Reason, r.DurationMs)
	}
	if !alive(pid) {
		t.Errorf("the server %d stopped with the script", pid)
	}
}

func TestATimeoutStopsTheChildProcessesOfTheCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "processes")
	h.start()
	id := h.fire("crawl")
	pid := h.pidIn("crawler.pid")
	if r := h.waitForRun(id); r.Status != "failed" || r.Reason != "timeout" {
		t.Errorf("crawl: %s %s", r.Status, r.Reason)
	}
	h.waitFor("the crawler stops", 20*time.Second, func() bool { return !alive(pid) })
}

func TestCancelStopsTheChildProcessesOfTheCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "processes")
	h.start()
	id := h.fire("crawl-until-canceled")
	pid := h.pidIn("crawler.pid")
	h.must("cancel", strconv.FormatInt(id, 10))
	if r := h.waitForRun(id); r.Status != "canceled" {
		t.Errorf("crawl-until-canceled: %s", r.Status)
	}
	h.waitFor("the crawler stops", 20*time.Second, func() bool { return !alive(pid) })
}
