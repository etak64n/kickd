//go:build e2e && windows

package windows

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Windows has no signal that asks a program to stop, so the server of
// these tests stops only when its stop command asks it to, and kickd ends
// its process tree once the stop command returns.

// waitForOutput waits until the output file of the server holds text.
func (h *home) waitForOutput(text string) {
	h.t.Helper()
	h.waitFor("server.log holds "+strconv.Quote(text), 60*time.Second, func() bool {
		b, _ := os.ReadFile(h.path("server.log"))
		return strings.Contains(string(b), text)
	})
}

// serverPID is the process ID that the server wrote.
func (h *home) serverPID() int {
	h.t.Helper()
	return readPID(h.t, h.path("server.pid"))
}

// readPID returns the process ID that a script wrote into the file at
// path.
func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF")))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestLongRunningCommandRestartsWhenItExits(t *testing.T) {
	t.Parallel()
	h := newHome(t, "long-running")
	h.start()
	h.waitForOutput("hello from startup")
	server, err := os.FindProcess(h.serverPID())
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Kill(); err != nil {
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

// Windows has no signal that asks a console program to stop, so kickd
// stops cleanly only as a service.
func TestServiceStopsALongRunningCommandThroughItsStopCommand(t *testing.T) {
	s := windowsService(t)
	id := s.fire("server")
	out := `C:\ProgramData\kickd\server.log`
	waitFor(t, "server.log holds hello from manual", 60*time.Second, func() bool {
		b, _ := os.ReadFile(out)
		return strings.Contains(string(b), "hello from manual")
	}, s.describe)
	pid := readPID(t, `C:\ProgramData\kickd\server.pid`)
	// A server that the stop left running would keep the folder of the
	// config, which the test removes.
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			p.Kill()
		}
	})
	s.mustKickd("service", "stop")
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "stopped by shutdown") {
		t.Errorf("server.log has no line stopped by shutdown:\n%s", b)
	}
	if alive(pid) {
		t.Errorf("the server %d still runs after the service stopped", pid)
	}
	if r := s.show(id); r.Status != "interrupted" {
		t.Errorf("server: %s %s", r.Status, r.Reason)
	}
}
