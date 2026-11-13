//go:build e2e && windows

package windows

import (
	"encoding/json"
	"sort"
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

func TestSkipRunsOnlyTheFirstOfTheFiringsThatWaitedWhileKickdWasStopped(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	backups := []int64{h.fire("backup"), h.fire("backup"), h.fire("backup")}
	h.start()
	if r := h.waitForRun(backups[0]); r.Status != "succeeded" || r.Skipped != 2 {
		t.Errorf("the first backup: %s, skipped %d", r.Status, r.Skipped)
	}
	for _, id := range backups[1:] {
		if r := h.waitForRun(id); r.Status != "skipped" || r.Reason != "already_running" {
			t.Errorf("backup %d: %s %s", id, r.Status, r.Reason)
		}
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

// cutOff fires event, waits until its command runs, and ends the agent, as
// a crash does. It returns the ID of the run.
func cutOff(h *home, event string) int64 {
	h.t.Helper()
	id := h.fire(event)
	h.waitFor(event+" runs", 30*time.Second, func() bool { return h.show(id).Status == "running" })
	h.kill()
	return id
}

func TestRerunRunsARunCutOffByACrashAgain(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	first := cutOff(h, "sync")
	h.start()
	rs := h.waitForRuns("sync", 1)
	if last := rs[len(rs)-1]; last.RetryOf != first || last.Attempt != 2 || last.Status != "succeeded" || last.line("attempt") != "2" {
		t.Errorf("the rerun: %+v\n%s", last, last.Output)
	}
}

// runs returns the runs of event, oldest first.
func (s *service) runs(event string) []run {
	s.t.Helper()
	var rs []run
	if err := json.Unmarshal([]byte(s.mustKickd("history", "--event", event, "--limit", "200", "--json")), &rs); err != nil {
		s.t.Fatal(err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

// cutOffByAStop fires event, waits until its command runs, stops the
// service, which cuts the run off, and starts the service again. It
// returns the ID of the run. Windows has no signal that asks a console
// program to stop, so kickd stops cleanly only as a service.
func cutOffByAStop(s *service, event string) int64 {
	s.t.Helper()
	id := s.fire(event)
	waitFor(s.t, event+" runs", 60*time.Second, func() bool { return s.show(id).Status == "running" }, s.describe)
	pid := s.agentPID()
	s.mustKickd("service", "stop")
	if r := s.show(id); r.Status != "interrupted" {
		s.t.Fatalf("%s after the stop of the service: %s %s", event, r.Status, r.Reason)
	}
	s.mustKickd("service", "start")
	s.waitForAgent(pid)
	return id
}

func TestServiceRerunRunsARunCutOffByAStopAgain(t *testing.T) {
	s := windowsService(t)
	first := cutOffByAStop(s, "sync")
	var last run
	waitFor(t, "the rerun of sync ends", 60*time.Second, func() bool {
		rs := s.runs("sync")
		last = s.show(rs[len(rs)-1].ID)
		return len(rs) == 2 && last.ended()
	}, s.describe)
	if last.RetryOf != first || last.Attempt != 2 || last.Status != "succeeded" || last.line("attempt") != "2" {
		t.Errorf("the rerun: %+v\n%s", last, last.Output)
	}
}

func TestServiceAbandonGivesUpARunCutOffByAStop(t *testing.T) {
	s := windowsService(t)
	id := cutOffByAStop(s, "release")
	if r := s.waitForRun(id); r.Status != "abandoned" || r.Reason != "agent_stopped" {
		t.Errorf("release: %s %s", r.Status, r.Reason)
	}
	if rs := s.runs("release"); len(rs) != 1 {
		t.Errorf("release ran again: %+v", rs)
	}
}

func TestAbandonGivesUpARunCutOffByACrash(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	id := cutOff(h, "release")
	h.start()
	if r := h.waitForRun(id); r.Status != "abandoned" || r.Reason != "agent_crashed" {
		t.Errorf("release: %s %s", r.Status, r.Reason)
	}
	if rs := h.runs("release"); len(rs) != 1 {
		t.Errorf("release ran again: %v", rs)
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
