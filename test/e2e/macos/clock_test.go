//go:build e2e && darwin

package macos

import (
	"os/exec"
	"testing"
	"time"
)

// moveTheClock moves the clock of the Mac by d, with date as root, and moves
// it back when the test ends.
func moveTheClock(t *testing.T, d time.Duration) {
	t.Helper()
	set := func(d time.Duration) {
		to := time.Now().Add(d).UTC()
		if out, err := exec.Command("sudo", "-n", "date", "-u", to.Format("010215042006.05")).CombinedOutput(); err != nil {
			t.Fatalf("date: %v\n%s", err, out)
		}
	}
	set(d)
	t.Cleanup(func() { set(-d) })
}

func TestClockMovingAheadDoesNotFireWake(t *testing.T) {
	changesTheMac(t)
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
	changesTheMac(t)
	h := newHome(t, "wake")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "Wake watch started", 1)
	moveTheClock(t, -2*time.Minute)
	time.Sleep(5 * time.Second)
	if rs := h.runs("resync"); len(rs) != 0 {
		t.Errorf("resync ran when the clock moved back: %v", rs)
	}
}
