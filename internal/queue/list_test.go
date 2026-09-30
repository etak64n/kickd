package queue

import (
	"errors"
	"testing"
	"time"
)

// ids returns the IDs of runs.
func ids(runs []Run) []int64 {
	out := make([]int64, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListRunsListsTheNewestRunFirst(t *testing.T) {
	s, _ := openTemp(t)
	a, _ := fire(s, t, "backup", "a", 0)
	b, _ := fire(s, t, "backup", "b", 0)
	c, _ := fire(s, t, "backup", "c", 0)
	runs, err := s.ListRuns(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(runs); !equalIDs(got, []int64{c, b, a}) {
		t.Errorf("got %v, want %v", got, []int64{c, b, a})
	}
}

func TestListRunsListsOnlyTheRunsOfTheEvent(t *testing.T) {
	s, _ := openTemp(t)
	backup, _ := fire(s, t, "backup", "a", 0)
	fire(s, t, "deploy", "b", 0)
	runs, err := s.ListRuns(ctx, Filter{Event: "backup"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(runs); !equalIDs(got, []int64{backup}) {
		t.Errorf("got %v, want %v", got, []int64{backup})
	}
}

func TestListRunsListsOnlyTheRunsWithTheStatuses(t *testing.T) {
	s, _ := openTemp(t)
	queued, _ := fire(s, t, "backup", "a", 0)
	running, _ := fire(s, t, "backup", "b", 0)
	done, _ := fire(s, t, "backup", "c", 0)
	s.StartRun(ctx, running, time.Now())
	s.StartRun(ctx, done, time.Now())
	s.FinishRun(ctx, done, Finish{Status: StatusSucceeded})
	runs, err := s.ListRuns(ctx, Filter{Statuses: []string{StatusQueued, StatusRunning}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(runs); !equalIDs(got, []int64{running, queued}) {
		t.Errorf("got %v, want %v", got, []int64{running, queued})
	}
}

func TestListRunsStopsAtTheLimit(t *testing.T) {
	s, _ := openTemp(t)
	for _, req := range []string{"a", "b", "c", "d"} {
		fire(s, t, "backup", req, 0)
	}
	runs, err := s.ListRuns(ctx, Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("got %d runs, want 2", len(runs))
	}
}

func TestGetRunReportsARunThatDoesNotExist(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.GetRun(ctx, 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRun(99): %v, want ErrNotFound", err)
	}
}

func TestCountsCountQueuedAndRunningRuns(t *testing.T) {
	s, _ := openTemp(t)
	fire(s, t, "backup", "a", 0)
	fire(s, t, "backup", "b", 0)
	running, _ := fire(s, t, "deploy", "c", 0)
	done, _ := fire(s, t, "deploy", "d", 0)
	s.StartRun(ctx, running, time.Now())
	s.StartRun(ctx, done, time.Now())
	s.FinishRun(ctx, done, Finish{Status: StatusFailed})
	queued, run, err := s.Counts(ctx)
	if err != nil || queued != 2 || run != 1 {
		t.Errorf("Counts() = %d queued, %d running, %v; want 2 and 1", queued, run, err)
	}
}

func TestSettleLeavesARunThatHasStarted(t *testing.T) {
	s, _ := openTemp(t)
	id, _ := fire(s, t, "backup", "a", 0)
	s.StartRun(ctx, id, time.Now())
	settled, err := s.Settle(ctx, id, StatusSkipped, "already_running", "")
	if err != nil || settled {
		t.Errorf("Settle of a running run: %v, %v", settled, err)
	}
	if r, _ := s.GetRun(ctx, id); r.Status != StatusRunning {
		t.Errorf("the run is %s, want running", r.Status)
	}
}

func TestAddSkippedCountsTheFiringsSkippedWhileTheRunWasActive(t *testing.T) {
	s, _ := openTemp(t)
	id, _ := fire(s, t, "backup", "a", 0)
	s.AddSkipped(ctx, id)
	s.AddSkipped(ctx, id)
	if r, _ := s.GetRun(ctx, id); r.Skipped != 2 {
		t.Errorf("skipped %d, want 2", r.Skipped)
	}
}

func TestFinishRunRecordsTheOutcomeOfTheRun(t *testing.T) {
	s, _ := openTemp(t)
	id, _ := fire(s, t, "backup", "a", 0)
	s.StartRun(ctx, id, time.Now())
	code := 23
	if err := s.FinishRun(ctx, id, Finish{Status: StatusFailed, Reason: "exit_code", ExitCode: &code, Output: "rsync: error\n",
		OutputTruncated: true, Duration: 1500 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFailed || r.Reason != "exit_code" || r.ExitCode == nil || *r.ExitCode != 23 || r.Output != "rsync: error\n" ||
		!r.OutputTruncated || r.Duration != 1500*time.Millisecond || r.FinishedAt.IsZero() {
		t.Errorf("the run: %+v", r)
	}
}

func TestFinalIsFalseOnlyForRunsThatCanStillChange(t *testing.T) {
	for status, want := range map[string]bool{
		StatusQueued: false, StatusRunning: false, StatusInterrupted: false,
		StatusSucceeded: true, StatusFailed: true, StatusCanceled: true, StatusSkipped: true,
		StatusDropped: true, StatusRetried: true, StatusAbandoned: true,
	} {
		if got := Final(status); got != want {
			t.Errorf("Final(%s) = %v, want %v", status, got, want)
		}
	}
}

func TestRequestCancelReportsARunThatDoesNotExist(t *testing.T) {
	s, _ := openTemp(t)
	if _, err := s.RequestCancel(ctx, 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("RequestCancel(99): %v, want ErrNotFound", err)
	}
}

func TestRequestCancelLeavesARunThatHasEnded(t *testing.T) {
	s, _ := openTemp(t)
	id, _ := fire(s, t, "backup", "a", 0)
	s.StartRun(ctx, id, time.Now())
	s.FinishRun(ctx, id, Finish{Status: StatusSucceeded})
	r, err := s.RequestCancel(ctx, id)
	if err != nil || r.Status != StatusSucceeded || r.CancelRequested {
		t.Errorf("RequestCancel of an ended run: %+v, %v", r, err)
	}
}

func TestRunsForRequestListsTheAttemptsOfAFiringOldestFirst(t *testing.T) {
	s, _ := openTemp(t)
	first, _ := fire(s, t, "sync", "req", 0)
	s.StartRun(ctx, first, time.Now())
	r, _ := s.GetRun(ctx, first)
	second, err := s.Retry(ctx, r, r.Payload, "agent_crashed")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.RunsForRequest(ctx, "req")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(runs); !equalIDs(got, []int64{first, second}) || runs[1].Attempt != 2 || runs[1].RetryOf != first {
		t.Errorf("the runs of the firing: %+v", runs)
	}
}

func TestAgentInfoReportsNoAgentBeforeTheFirstHeartbeat(t *testing.T) {
	s, _ := openTemp(t)
	if _, known, err := s.AgentInfo(ctx); known || err != nil {
		t.Errorf("AgentInfo() = known %v, %v", known, err)
	}
}

func TestAliveIsFalseAfterTheAgentStopped(t *testing.T) {
	s, _ := openTemp(t)
	s.Heartbeat(ctx, Agent{PID: 42, Version: "v1", Host: "h", StartedAt: time.Now()})
	s.AgentStopped(ctx)
	a, _, err := s.AgentInfo(ctx)
	if err != nil || a.Alive(time.Minute) || a.StoppedAt.IsZero() {
		t.Errorf("the agent after a stop: %+v, %v", a, err)
	}
}

func TestAliveIsFalseWhenTheLastHeartbeatIsOlderThanTheWindow(t *testing.T) {
	a := Agent{HeartbeatAt: time.Now().Add(-time.Minute)}
	if a.Alive(30 * time.Second) {
		t.Error("an agent silent for a minute is alive within 30 seconds")
	}
	if !a.Alive(2 * time.Minute) {
		t.Error("an agent silent for a minute is not alive within 2 minutes")
	}
}
