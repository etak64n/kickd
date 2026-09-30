//go:build e2e && windows

package windows

import (
	"testing"
	"time"
)

func TestCronFiresAtTheSameMomentInEveryTimeZone(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cron")
	h.start()
	for _, event := range []string{"in-utc", "in-tokyo", "in-kolkata", "in-kathmandu", "in-st-johns"} {
		r := h.waitForRuns(event, 1)[0]
		if r.line("trigger") != "cron" || r.line("scheduled") != h.at.Format(time.RFC3339) {
			t.Errorf("%s: scheduled at %q, want %s", event, r.line("scheduled"), h.at.Format(time.RFC3339))
		}
		if late := r.started().Sub(h.at); late < 0 || late > 5*time.Second {
			t.Errorf("%s: started %s after the scheduled time", event, late)
		}
	}
}

// stopOverTheScheduledTime starts kickd, which records when it last looked
// at the schedules, ends it, and waits until the scheduled time is more
// than a minute past, which makes it a missed time.
func stopOverTheScheduledTime(h *home) {
	h.t.Helper()
	h.start()
	time.Sleep(2 * time.Second)
	h.kill()
	if !time.Now().Before(h.at) {
		h.t.Fatal("kickd ended after the scheduled time")
	}
	time.Sleep(time.Until(h.at.Add(65 * time.Second)))
}

func TestCronRunsATimeMissedWhileStopped(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cron-missed")
	stopOverTheScheduledTime(h)
	h.start()
	r := h.waitForRuns("backup", 1)[0]
	if r.line("missed") != "1" || r.line("scheduled") != h.at.Format(time.RFC3339) {
		t.Errorf("backup: missed %q, scheduled %q\n%s", r.line("missed"), r.line("scheduled"), r.Output)
	}
}

func TestCronSkipsATimeMissedWhileStoppedWithSkip(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cron-missed")
	stopOverTheScheduledTime(h)
	h.start()
	h.waitForRuns("backup", 1)
	if rs := h.runs("digest"); len(rs) != 0 {
		t.Errorf("digest ran: %v", rs)
	}
}
