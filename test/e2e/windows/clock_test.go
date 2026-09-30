//go:build e2e && windows

package windows

import (
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// moveTheClock moves the clock of the machine by d with Set-Date, and moves
// it back when the test ends.
func moveTheClock(t *testing.T, d time.Duration) {
	t.Helper()
	set := func(d time.Duration) {
		adjust := fmt.Sprintf("Set-Date -Adjust ([TimeSpan]::FromSeconds(%d)) | Out-Null", int(d.Seconds()))
		if out, err := exec.Command("powershell", "-NoProfile", "-Command", adjust).CombinedOutput(); err != nil {
			t.Fatalf("Set-Date: %v\n%s", err, out)
		}
	}
	set(d)
	t.Cleanup(func() { set(-d) })
}

func TestClockMovingAheadDoesNotFireWake(t *testing.T) {
	changesTheMachine(t)
	h := newHome(t, "wake")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "Wake watch started", 1)
	moveTheClock(t, 2*time.Minute)
	time.Sleep(5 * time.Second)
	if rs := h.runs("resync"); len(rs) != 0 {
		t.Errorf("resync ran when the clock moved ahead: %v", rs)
	}
}

func TestClockMovingBackDoesNotFireWake(t *testing.T) {
	changesTheMachine(t)
	h := newHome(t, "wake")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "Wake watch started", 1)
	moveTheClock(t, -2*time.Minute)
	time.Sleep(5 * time.Second)
	if rs := h.runs("resync"); len(rs) != 0 {
		t.Errorf("resync ran when the clock moved back: %v", rs)
	}
}
