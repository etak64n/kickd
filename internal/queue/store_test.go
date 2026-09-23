package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "kickd.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func fire(s *Store, t *testing.T, event, req string, max int) (int64, bool) {
	t.Helper()
	id, dropped, err := s.Enqueue(ctx, Run{RequestID: req, Event: event, Trigger: "cron", TriggerID: "cron:@hourly", Payload: []byte(`{"event":"` + event + `"}`)}, max)
	if err != nil {
		t.Fatal(err)
	}
	return id, dropped
}

func TestOpenCreatesPrivateFileAndIsIdempotent(t *testing.T) {
	s, path := openTemp(t)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	again, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	again.Close()
	if s.Path() != path {
		t.Errorf("Path = %q", s.Path())
	}
}

func TestEnqueueLifecycle(t *testing.T) {
	s, _ := openTemp(t)
	a, _ := fire(s, t, "backup", "r1", 0)
	b, _ := fire(s, t, "backup", "r2", 0)
	queued, err := s.QueuedRuns(ctx, 10)
	if err != nil || len(queued) != 2 || queued[0].ID != a || queued[1].ID != b || queued[0].Attempt != 1 {
		t.Fatalf("queued = %+v, err = %v", queued, err)
	}
	if ok, _ := s.StartRun(ctx, a, time.Now()); !ok {
		t.Fatal("StartRun failed")
	}
	if ok, _ := s.StartRun(ctx, a, time.Now()); ok {
		t.Fatal("a running run must not start twice")
	}
	if err := s.AddSkipped(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Settle(ctx, b, StatusSkipped, "already_running", "run 1"); !ok {
		t.Fatal("Settle failed")
	}
	code := 3
	if err := s.FinishRun(ctx, a, Finish{Status: StatusFailed, Reason: "exit_code", ExitCode: &code, Output: "boom\n", Duration: 1500 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRun(ctx, a)
	if err != nil || r.Status != StatusFailed || r.ExitCode == nil || *r.ExitCode != 3 || r.Output != "boom\n" || r.Skipped != 1 ||
		r.Duration != 1500*time.Millisecond || r.StartedAt.IsZero() || r.FinishedAt.IsZero() || r.Open() || !Final(r.Status) {
		t.Fatalf("run = %+v, err = %v", r, err)
	}
	if r, _ := s.GetRun(ctx, b); r.Status != StatusSkipped || r.Detail != "run 1" || r.FinishedAt.IsZero() {
		t.Errorf("skipped = %+v", r)
	}
	if list, _ := s.ListRuns(ctx, Filter{Event: "backup", Statuses: []string{StatusFailed}}); len(list) != 1 || list[0].ID != a {
		t.Errorf("ListRuns = %+v", list)
	}
	if _, err := s.GetRun(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing run: %v", err)
	}
}

func TestEnqueueRecordsDropsWhenFull(t *testing.T) {
	s, _ := openTemp(t)
	fire(s, t, "sync", "a", 2)
	fire(s, t, "sync", "b", 2)
	id, dropped := fire(s, t, "sync", "c", 2)
	if !dropped {
		t.Fatal("third firing must be dropped")
	}
	r, _ := s.GetRun(ctx, id)
	if r.Status != StatusDropped || r.Reason != "queue_full" || r.FinishedAt.IsZero() {
		t.Errorf("dropped row = %+v", r)
	}
	if _, dropped := fire(s, t, "other", "d", 2); dropped {
		t.Error("the limit is per event")
	}
}

func TestInterruptedRunsRetryAndAbandon(t *testing.T) {
	s, _ := openTemp(t)
	crashed, _ := fire(s, t, "backup", "req-crash", 0)
	s.StartRun(ctx, crashed, time.Now())
	stopped, _ := fire(s, t, "report", "req-stop", 0)
	s.StartRun(ctx, stopped, time.Now())
	s.FinishRun(ctx, stopped, Finish{Status: StatusInterrupted, Reason: "shutdown"})
	done, _ := fire(s, t, "backup", "req-done", 0)
	s.StartRun(ctx, done, time.Now())
	s.FinishRun(ctx, done, Finish{Status: StatusSucceeded})

	got, err := s.InterruptedRuns(ctx)
	if err != nil || len(got) != 2 || got[0].ID != crashed || got[1].ID != stopped {
		t.Fatalf("interrupted = %+v, err = %v", got, err)
	}
	next, err := s.Retry(ctx, got[0], []byte(`{"attempt":2}`), "agent_crashed")
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetRun(ctx, crashed)
	if old.Status != StatusRetried || old.Reason != "agent_crashed" || old.Detail == "" {
		t.Errorf("old = %+v", old)
	}
	n, _ := s.GetRun(ctx, next)
	if n.Status != StatusQueued || n.Attempt != 2 || n.RetryOf != crashed || n.RequestID != "req-crash" || string(n.Payload) != `{"attempt":2}` {
		t.Errorf("next = %+v", n)
	}
	if r, ok, _ := s.RetryOf(ctx, crashed); !ok || r.ID != next {
		t.Errorf("RetryOf = %+v, %v", r, ok)
	}
	if err := s.Abandon(ctx, stopped, "on_interrupt_abandon"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetRun(ctx, stopped); r.Status != StatusAbandoned || r.Reason != "on_interrupt_abandon" {
		t.Errorf("abandoned = %+v", r)
	}
	if left, _ := s.InterruptedRuns(ctx); len(left) != 0 {
		t.Errorf("left = %+v", left)
	}
}

func TestCancelQueuedAndRunning(t *testing.T) {
	s, _ := openTemp(t)
	q, _ := fire(s, t, "a", "q", 0)
	r, _ := fire(s, t, "b", "r", 0)
	s.StartRun(ctx, r, time.Now())
	got, err := s.RequestCancel(ctx, q)
	if err != nil || got.Status != StatusCanceled || got.Reason != "canceled_by_user" {
		t.Fatalf("cancel queued = %+v, %v", got, err)
	}
	if ok, _ := s.StartRun(ctx, q, time.Now()); ok {
		t.Fatal("a canceled run must not start")
	}
	got, err = s.RequestCancel(ctx, r)
	if err != nil || got.Status != StatusRunning || !got.CancelRequested {
		t.Fatalf("cancel running = %+v, %v", got, err)
	}
	if ids, _ := s.CancelRequests(ctx); len(ids) != 1 || ids[0] != r {
		t.Fatalf("CancelRequests = %v", ids)
	}
	s.FinishRun(ctx, r, Finish{Status: StatusCanceled, Reason: "canceled_by_user"})
	if ids, _ := s.CancelRequests(ctx); len(ids) != 0 {
		t.Fatalf("CancelRequests after finish = %v", ids)
	}
	if _, err := s.RequestCancel(ctx, 12345); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestPruneKeepsOpenAndInterruptedRuns(t *testing.T) {
	s, _ := openTemp(t)
	old, _ := fire(s, t, "a", "old", 0)
	s.StartRun(ctx, old, time.Now())
	s.FinishRun(ctx, old, Finish{Status: StatusSucceeded})
	queued, _ := fire(s, t, "a", "queued", 0)
	cut, _ := fire(s, t, "a", "cut", 0)
	s.StartRun(ctx, cut, time.Now())
	s.FinishRun(ctx, cut, Finish{Status: StatusInterrupted, Reason: "shutdown"})
	n, err := s.Prune(ctx, time.Now().Add(time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v", n, err)
	}
	for _, id := range []int64{queued, cut} {
		if _, err := s.GetRun(ctx, id); err != nil {
			t.Errorf("run %d was pruned: %v", id, err)
		}
	}
}

func TestDataVersionSeesOtherConnections(t *testing.T) {
	daemon, path := openTemp(t)
	v1, err := daemon.DataVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fire(daemon, t, "own", "x", 0)
	if v, _ := daemon.DataVersion(ctx); v != v1 {
		t.Errorf("own write moved data_version: %d -> %d", v1, v)
	}
	cli, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	fire(cli, t, "deploy", "y", 0)
	if v, _ := daemon.DataVersion(ctx); v == v1 {
		t.Error("data_version did not change after another connection wrote")
	}
}

func TestConcurrentWritersFromTwoConnections(t *testing.T) {
	a, path := openTemp(t)
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, _, err := s.Enqueue(ctx, Run{RequestID: "x", Event: "e", Trigger: "cron", TriggerID: "c", Payload: []byte(`{}`)}, 0); err != nil {
					errs <- err
				}
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if n, _ := a.CountQueued(ctx, "e"); n != 200 {
		t.Fatalf("rows = %d, want 200", n)
	}
}

func TestHeartbeat(t *testing.T) {
	s, _ := openTemp(t)
	if _, ok, _ := s.AgentInfo(ctx); ok {
		t.Fatal("no agent expected")
	}
	start := time.Now().Add(-time.Minute)
	if err := s.Heartbeat(ctx, Agent{PID: 42, Version: "v", Host: "h", StartedAt: start}); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.AgentInfo(ctx)
	if err != nil || !ok || a.PID != 42 || !a.Alive(15*time.Second) || a.StartedAt.UnixMilli() != start.UnixMilli() {
		t.Fatalf("agent = %+v, %v", a, err)
	}
	s.AgentStopped(ctx)
	if a, _, _ := s.AgentInfo(ctx); a.Alive(15 * time.Second) {
		t.Error("stopped agent must not be alive")
	}
}
