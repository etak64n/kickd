//go:build e2e && linux

package linux

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitForOutput waits until the output file of the server holds text.
func (h *home) waitForOutput(text string) {
	h.t.Helper()
	h.waitFor("server.log holds "+strconv.Quote(text), 30*time.Second, func() bool {
		b, _ := os.ReadFile(h.path("server.log"))
		return strings.Contains(string(b), text)
	})
}

// serverPID is the process ID that the server wrote.
func (h *home) serverPID() int {
	h.t.Helper()
	b, err := os.ReadFile(h.path("server.pid"))
	if err != nil {
		h.t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		h.t.Fatal(err)
	}
	return pid
}

func TestLongRunningCommandRestartsWhenItExits(t *testing.T) {
	t.Parallel()
	h := newHome(t, "long-running")
	h.start()
	h.waitForOutput("hello from startup")
	if err := syscall.Kill(h.serverPID(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.waitForOutput("hello from restart")
	rs := h.runs("server")
	if len(rs) < 2 || rs[0].Status != "failed" || rs[1].Trigger != "restart" {
		t.Fatalf("runs = %+v", rs)
	}
}

func TestLongRunningCommandIsReplacedThroughItsStopCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "long-running")
	h.start()
	h.waitForOutput("hello from startup")
	h.fire("server")
	h.waitForOutput("stopped by replaced")
	h.waitForOutput("hello from manual")
	first := h.waitForRun(h.runs("server")[0].ID)
	if first.Status != "canceled" || first.Reason != "replaced" {
		t.Errorf("replaced run = %+v", first)
	}
	time.Sleep(time.Second)
	for _, r := range h.runs("server") {
		if r.Trigger == "restart" {
			t.Errorf("a replaced run was restarted: %+v", r)
		}
	}
}

func TestLongRunningCommandStopsWithKickdThroughItsStopCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "long-running")
	h.start()
	h.waitForOutput("hello from startup")
	pid := h.serverPID()
	h.stop()
	h.waitForOutput("stopped by shutdown")
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the server %d still runs after kickd stopped", pid)
	}
	if r := h.runs("server")[0]; r.Status != "interrupted" {
		t.Errorf("run = %+v", r)
	}
}
