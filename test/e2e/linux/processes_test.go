//go:build e2e && linux

package linux

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pidIn waits for the process ID that a script wrote into the file name in
// the home directory, and ends that process when the test ends.
func (h *home) pidIn(name string) int {
	h.t.Helper()
	var pid int
	h.waitFor(name+" is written", 30*time.Second, func() bool {
		b, err := os.ReadFile(h.path(name))
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && pid > 0
	})
	h.t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// alive reports whether the process pid exists.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestABackgroundProcessWithItsOutputRedirectedKeepsRunning(t *testing.T) {
	t.Parallel()
	h := newHome(t, "processes")
	h.start()
	r := h.waitForRun(h.fire("start-server"))
	pid := h.pidIn("server.pid")
	if r.Status != "succeeded" || r.DurationMs > 5000 {
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
