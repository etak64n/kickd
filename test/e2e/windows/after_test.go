//go:build e2e && windows

package windows

import (
	"strconv"
	"testing"
	"time"
)

func TestAfterFiresWhenTheFollowedRunFails(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	h.start()
	check := h.fire("check")
	r := h.waitForRuns("alert", 1)[0]
	if r.line("trigger") != "after" || r.line("after") != "check" || r.line("run") != strconv.FormatInt(check, 10) ||
		r.line("status") != "failed" || r.line("exit") != "3" {
		t.Errorf("alert:\n%s", r.Output)
	}
}

func TestAfterDoesNotFireForAStatusThatIsNotListed(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	h.start()
	if r := h.waitForRun(h.fire("check", "code=0")); r.Status != "succeeded" {
		t.Fatalf("check: %s", r.Status)
	}
	time.Sleep(2 * time.Second)
	if rs := h.runs("alert"); len(rs) != 0 {
		t.Errorf("alert ran after a check that succeeded: %v", rs)
	}
}

func TestAfterFiresForARunThatKickdCancelCanceledWhileItWaited(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	// Without the agent, the run waits in the queue until kickd cancel.
	check := h.fire("check")
	h.must("cancel", strconv.FormatInt(check, 10))
	h.start()
	r := h.waitForRuns("alert", 1)[0]
	if r.line("status") != "canceled" || r.line("run") != strconv.FormatInt(check, 10) {
		t.Errorf("alert:\n%s", r.Output)
	}
}

func TestAfterFollowsAnEventThatAnAfterTriggerFired(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	h.start()
	h.fire("check")
	r := h.waitForRuns("report", 1)[0]
	if r.line("trigger") != "after" || r.line("after") != "alert" {
		t.Errorf("report:\n%s", r.Output)
	}
}

func TestAfterFiresWhenTheFollowedRunIsAbandoned(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	h.start()
	backup := h.fire("backup")
	h.waitFor("backup runs", 30*time.Second, func() bool { return h.show(backup).Status == "running" })
	h.kill()
	h.start()
	r := h.waitForRuns("notify", 1)[0]
	if r.line("after") != "backup" || r.line("run") != strconv.FormatInt(backup, 10) || r.line("status") != "abandoned" || !hasLine(r.Output, "exit=") {
		t.Errorf("notify:\n%s", r.Output)
	}
}

func TestAfterFiresWhenTheFollowedRunIsSkipped(t *testing.T) {
	t.Parallel()
	h := newHome(t, "after")
	h.start()
	first := h.fire("backup")
	h.waitFor("backup runs", 30*time.Second, func() bool { return h.show(first).Status == "running" })
	second := h.fire("backup")
	r := h.waitForRuns("notify", 1)[0]
	if r.line("after") != "backup" || r.line("run") != strconv.FormatInt(second, 10) || r.line("status") != "skipped" || !hasLine(r.Output, "exit=") {
		t.Errorf("notify:\n%s", r.Output)
	}
}
