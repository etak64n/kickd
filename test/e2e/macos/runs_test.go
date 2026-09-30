//go:build e2e && darwin

package macos

import (
	"strconv"
	"testing"
	"time"
)

func TestQueueRunsFiringsOneAfterAnother(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	for range 3 {
		h.fire("deploy")
	}
	rs := h.waitForRuns("deploy", 3)
	for i := 1; i < len(rs); i++ {
		if rs[i].started().Before(rs[i-1].finished()) {
			t.Errorf("deploy %d started before deploy %d finished", rs[i].ID, rs[i-1].ID)
		}
	}
}

func TestSkipSkipsAFiringWhileTheEventRuns(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	first := h.fire("backup")
	h.waitFor("backup runs", 30*time.Second, func() bool { return h.show(first).Status == "running" })
	second := h.fire("backup")
	if r := h.waitForRun(second); r.Status != "skipped" || r.Reason != "already_running" {
		t.Errorf("the second backup: %s %s", r.Status, r.Reason)
	}
	if r := h.waitForRun(first); r.Status != "succeeded" || r.Skipped != 1 {
		t.Errorf("the first backup: %s, skipped %d", r.Status, r.Skipped)
	}
}

func TestParallelRunsFiringsAtTheSameTime(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	first, second := h.fire("notify"), h.fire("notify")
	a, b := h.waitForRun(first), h.waitForRun(second)
	if !b.started().Before(a.finished()) {
		t.Errorf("the second notification started after the first one finished")
	}
}

// cutOff fires event, waits until its command runs, and cuts the run off
// with how, "crash" or "stop". It returns the ID of the run.
func cutOff(h *home, event, how string) int64 {
	h.t.Helper()
	id := h.fire(event)
	h.waitFor(event+" runs", 30*time.Second, func() bool { return h.show(id).Status == "running" })
	if how == "crash" {
		h.kill()
	} else {
		h.stop()
	}
	return id
}

func TestRerunRunsARunCutOffByACrashAgain(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	first := cutOff(h, "sync", "crash")
	h.start()
	rs := h.waitForRuns("sync", 1)
	if last := rs[len(rs)-1]; last.RetryOf != first || last.Attempt != 2 || last.Status != "succeeded" || last.line("attempt") != "2" {
		t.Errorf("the rerun: %+v\n%s", last, last.Output)
	}
}

func TestRerunRunsARunCutOffByAStopAgain(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	first := cutOff(h, "sync", "stop")
	h.start()
	rs := h.waitForRuns("sync", 1)
	if last := rs[len(rs)-1]; last.RetryOf != first || last.Attempt != 2 || last.Status != "succeeded" {
		t.Errorf("the rerun: %+v", last)
	}
}

func TestAbandonGivesUpARunCutOffByACrash(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	id := cutOff(h, "release", "crash")
	h.start()
	if r := h.waitForRun(id); r.Status != "abandoned" || r.Reason != "agent_crashed" {
		t.Errorf("release: %s %s", r.Status, r.Reason)
	}
	if rs := h.runs("release"); len(rs) != 1 {
		t.Errorf("release ran again: %v", rs)
	}
}

func TestAbandonGivesUpARunCutOffByAStop(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	id := cutOff(h, "release", "stop")
	h.start()
	if r := h.waitForRun(id); r.Status != "abandoned" || r.Reason != "agent_stopped" {
		t.Errorf("release: %s %s", r.Status, r.Reason)
	}
}

func TestTimeoutStopsTheCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	r := h.waitForRun(h.fire("report"))
	if r.Status != "failed" || r.Reason != "timeout" || r.DurationMs > 15000 {
		t.Errorf("report: %s %s after %d ms", r.Status, r.Reason, r.DurationMs)
	}
}

func TestCancelStopsTheCommand(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	id := h.fire("import")
	h.waitFor("import runs", 30*time.Second, func() bool { return h.show(id).Status == "running" })
	h.must("cancel", strconv.FormatInt(id, 10))
	if r := h.waitForRun(id); r.Status != "canceled" || r.DurationMs > 15000 {
		t.Errorf("import: %s after %d ms", r.Status, r.DurationMs)
	}
}
