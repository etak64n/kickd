//go:build e2e && linux

package linux

import (
	"testing"
	"time"
)

// A runner of the CI cannot sleep, so these tests check what happens while
// the machine is awake. The unit tests of internal/trigger check a sleep
// with a clock that the test moves.

func TestWakeDoesNotFireWhileTheMachineIsAwake(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wake")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "Wake watch started", 1)
	time.Sleep(5 * time.Second)
	if rs := h.runs("resync"); len(rs) != 0 {
		t.Errorf("resync ran while the machine was awake: %v", rs)
	}
}
