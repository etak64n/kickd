package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
)

type dispEnv struct {
	r     *Runner
	d     *Dispatcher
	store *queue.Store
	rec   *recorder
	stop  func()
}

func newDispatcher(t *testing.T, specs ...Spec) *dispEnv {
	t.Helper()
	return startDispatcher(t, filepath.Join(t.TempDir(), "kickd.db"), specs...)
}

// startDispatcher configures a dispatcher over path, recovers interrupted
// runs and starts its loop. stop (also run at cleanup) cancels running
// commands as a shutdown does.
func startDispatcher(t *testing.T, path string, specs ...Spec) *dispEnv {
	t.Helper()
	store, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	base := slog.New(&recHandler{root: rec})
	r := New(ctx, base, "proc")
	d := NewDispatcher(r, store, base.With("requestId", "proc"), time.Hour)
	d.Configure(specs)
	if err := d.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	loopCtx, stopLoop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Loop(loopCtx, AgentInfo("test", time.Now()))
	}()
	stopped := false
	env := &dispEnv{r: r, d: d, store: store, rec: rec}
	env.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		r.Wait()
		stopLoop()
		<-done
		store.Close()
	}
	t.Cleanup(env.stop)
	return env
}

func fired(name string) event.Event {
	return event.Event{Name: name, Trigger: event.KindCron, TriggerID: "cron:@every 1s", Time: time.Now(), Cron: &event.CronInfo{Schedule: "@every 1s"}}
}

func waitRun(t *testing.T, s *queue.Store, id int64, status string) queue.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r, err := s.GetRun(context.Background(), id)
		if err == nil && r.Status == status {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d: status %q, want %q (err %v)", id, r.Status, status, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func runOf(t *testing.T, s *queue.Store, requestID string) queue.Run {
	t.Helper()
	runs, err := s.RunsForRequest(context.Background(), requestID)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs for %s = %+v, %v", requestID, runs, err)
	}
	return runs[len(runs)-1]
}

func TestSkipPolicySettlesQueuedFirings(t *testing.T) {
	spec := helperJob("short")
	env := newDispatcher(t, spec)
	h := env.d.Handler(spec.Name)
	first := fired(spec.Name)
	first.RequestID = "run-1"
	if st := h.Dispatch(first); st != event.Queued {
		t.Fatalf("dispatch = %s", st)
	}
	env.rec.waitFor(t, "Run started", 1, 10*time.Second)
	for _, id := range []string{"s1", "s2"} {
		ev := fired(spec.Name)
		ev.RequestID = id
		h.Dispatch(ev)
	}
	for _, id := range []string{"s1", "s2"} {
		r := waitRun(t, env.store, runOf(t, env.store, id).ID, queue.StatusSkipped)
		if r.Reason != "already_running" || !strings.HasPrefix(r.Detail, "run ") {
			t.Errorf("skipped row = %+v", r)
		}
	}
	if _, err := h.RunSync(context.Background(), fired(spec.Name)); !errors.Is(err, event.ErrBusy) {
		t.Fatalf("RunSync = %v, want ErrBusy", err)
	}
	skipped := env.rec.find("Run skipped, previous run still active")
	if len(skipped) != 3 || skipped[0]["level"] != slog.LevelWarn || skipped[1]["level"] != slog.LevelDebug || skipped[0]["activeRequestId"] != "run-1" {
		t.Fatalf("skip lines = %v", skipped)
	}
	completed := env.rec.waitFor(t, "Run completed", 1, 10*time.Second)
	if completed[0]["skipped"] != int64(3) {
		t.Errorf("completed = %v", completed[0])
	}
	if r := waitRun(t, env.store, runOf(t, env.store, "run-1").ID, queue.StatusSucceeded); r.Skipped != 3 {
		t.Errorf("active row = %+v", r)
	}
}

func TestQueuePolicyRunsInOrder(t *testing.T) {
	spec := helperJob("env")
	spec.Concurrency = PolicyQueue
	env := newDispatcher(t, spec)
	h := env.d.Handler(spec.Name)
	for i := 0; i < 3; i++ {
		h.Dispatch(fired(spec.Name))
	}
	env.rec.waitFor(t, "Run completed", 3, 20*time.Second)
	var order []string
	env.rec.mu.Lock()
	for _, m := range env.rec.records {
		if m["message"] == "Run started" || m["message"] == "Run completed" {
			order = append(order, m["message"].(string))
		}
	}
	env.rec.mu.Unlock()
	if got := strings.Join(order, ","); got != "Run started,Run completed,Run started,Run completed,Run started,Run completed" {
		t.Fatalf("order = %s", got)
	}
}

func TestParallelPolicy(t *testing.T) {
	spec := helperJob("sleep")
	spec.Concurrency = PolicyParallel
	env := newDispatcher(t, spec)
	h := env.d.Handler(spec.Name)
	h.Dispatch(fired(spec.Name))
	h.Dispatch(fired(spec.Name))
	env.rec.waitFor(t, "Run started", 2, 10*time.Second)
	if n := env.r.InFlight(); n != 2 {
		t.Errorf("InFlight = %d", n)
	}
}

func TestRunSyncAndDefaults(t *testing.T) {
	spec := helperJob("exit")
	spec.Defaults = map[string]string{"ref": "main"}
	env := newDispatcher(t, spec)
	ev := fired(spec.Name)
	ev.RequestID = "sync"
	res, err := env.d.Handler(spec.Name).RunSync(context.Background(), ev)
	if err != nil || res.ExitCode != 3 || res.Reason != "exit_code" || !strings.Contains(res.Output, "failing on purpose") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	var payload event.Event
	json.Unmarshal(runOf(t, env.store, "sync").Payload, &payload)
	if payload.Data["ref"] != "main" {
		t.Errorf("defaults not applied to the payload: %+v", payload.Data)
	}
}

func TestCallerLeavesRunContinues(t *testing.T) {
	spec := helperJob("short")
	env := newDispatcher(t, spec)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ev := fired(spec.Name)
	ev.RequestID = "req-gone"
	if _, err := env.d.Handler(spec.Name).RunSync(ctx, ev); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunSync = %v", err)
	}
	waitRun(t, env.store, runOf(t, env.store, "req-gone").ID, queue.StatusSucceeded)
}

func TestManualEnqueueFromAnotherProcess(t *testing.T) {
	spec := helperJob("env")
	env := newDispatcher(t, spec)
	// "kickd event" writes the row from another process; the loop notices it.
	cli, err := queue.Open(env.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ev := event.Event{RequestID: "from-cli", Name: spec.Name, Trigger: event.KindManual, TriggerID: "manual", Source: "alice@laptop",
		Time: time.Now(), Data: map[string]string{"ref": "v1.2"}}
	payload, _ := json.Marshal(ev)
	id, _, err := cli.Enqueue(context.Background(), queue.Run{RequestID: ev.RequestID, Event: spec.Name, Trigger: event.KindManual,
		TriggerID: "manual", Source: ev.Source, Payload: payload}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, env.store, id, queue.StatusSucceeded)
	if !strings.Contains(r.Output, "KICKD_TRIGGER=manual") || !strings.Contains(r.Output, `"data":{"ref":"v1.2"}`) {
		t.Errorf("output:\n%s", r.Output)
	}
	started := env.rec.find("Run started")
	if len(started) != 1 || started[0]["source"] != "alice@laptop" || started[0]["requestId"] != "from-cli" {
		t.Errorf("started = %v", started)
	}
}

func TestCancelQueuedAndRunning(t *testing.T) {
	spec := helperJob("sleep")
	spec.Concurrency = PolicyQueue
	env := newDispatcher(t, spec)
	h := env.d.Handler(spec.Name)
	first, second := fired(spec.Name), fired(spec.Name)
	first.RequestID, second.RequestID = "first", "second"
	h.Dispatch(first)
	h.Dispatch(second)
	env.rec.waitFor(t, "Run started", 1, 10*time.Second)
	ctx := context.Background()
	if r, err := env.store.RequestCancel(ctx, runOf(t, env.store, "second").ID); err != nil || r.Status != queue.StatusCanceled {
		t.Fatalf("cancel queued = %+v, %v", r, err)
	}
	running := runOf(t, env.store, "first")
	if r, err := env.store.RequestCancel(ctx, running.ID); err != nil || !r.CancelRequested {
		t.Fatalf("cancel running = %+v, %v", r, err)
	}
	if r := waitRun(t, env.store, running.ID, queue.StatusCanceled); r.Reason != "canceled_by_user" {
		t.Errorf("reason = %q", r.Reason)
	}
	if c := env.rec.find("Run canceled"); len(c) != 1 || c[0]["reason"] != "canceled_by_user" {
		t.Errorf("canceled = %v", c)
	}
	time.Sleep(300 * time.Millisecond)
	if len(env.rec.find("Run started")) != 1 {
		t.Error("the canceled queued run must not start")
	}
}

func TestRunsOfRemovedEventsAreDropped(t *testing.T) {
	spec := helperJob("sleep")
	spec.Concurrency = PolicyQueue
	env := newDispatcher(t, spec)
	h := env.d.Handler(spec.Name)
	h.Dispatch(fired(spec.Name))
	queued := fired(spec.Name)
	queued.RequestID = "queued"
	h.Dispatch(queued)
	env.rec.waitFor(t, "Run started", 1, 10*time.Second)
	env.d.Configure(nil)
	if r := waitRun(t, env.store, runOf(t, env.store, "queued").ID, queue.StatusDropped); r.Reason != "event_removed" {
		t.Errorf("reason = %q", r.Reason)
	}
}

func TestShutdownMarksRunsInterrupted(t *testing.T) {
	spec := helperJob("sleep")
	path := filepath.Join(t.TempDir(), "kickd.db")
	env := startDispatcher(t, path, spec)
	ev := fired(spec.Name)
	ev.RequestID = "cut"
	env.d.Handler(spec.Name).Dispatch(ev)
	env.rec.waitFor(t, "Run started", 1, 10*time.Second)
	id := runOf(t, env.store, "cut").ID
	env.stop()

	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, _ := s.GetRun(context.Background(), id)
	if r.Status != queue.StatusInterrupted || r.Reason != "shutdown" {
		t.Fatalf("run = %+v", r)
	}
}

// interrupted leaves one run of spec in the database as a crash would:
// still marked running, with no process behind it.
func interrupted(t *testing.T, path string, spec Spec, requestID string, attempt int) int64 {
	t.Helper()
	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ev := event.Event{RequestID: requestID, Name: spec.Name, Trigger: event.KindManual, TriggerID: "manual", Attempt: attempt, Time: time.Now()}
	payload, _ := json.Marshal(ev)
	id, _, err := s.Enqueue(context.Background(), queue.Run{RequestID: requestID, Event: spec.Name, Trigger: event.KindManual,
		TriggerID: "manual", Payload: payload, Attempt: attempt}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.StartRun(context.Background(), id, time.Now()); !ok || err != nil {
		t.Fatalf("StartRun: %v %v", ok, err)
	}
	return id
}

func TestRecoverRerunsInterruptedRuns(t *testing.T) {
	spec := helperJob("env")
	spec.OnInterrupt = InterruptRerun
	path := filepath.Join(t.TempDir(), "kickd.db")
	crashed := interrupted(t, path, spec, "crashed", 1)

	env := startDispatcher(t, path, spec)
	old, _ := env.store.GetRun(context.Background(), crashed)
	if old.Status != queue.StatusRetried || old.Reason != "agent_crashed" {
		t.Fatalf("old = %+v", old)
	}
	next, ok, _ := env.store.RetryOf(context.Background(), crashed)
	if !ok {
		t.Fatal("no rerun")
	}
	r := waitRun(t, env.store, next.ID, queue.StatusSucceeded)
	if r.Attempt != 2 || r.RequestID != "crashed" || !strings.Contains(r.Output, "KICKD_ATTEMPT=2") {
		t.Errorf("rerun = %+v\n%s", r, r.Output)
	}
	if rq := env.rec.find("Interrupted run requeued"); len(rq) != 1 || rq[0]["attempt"] != int64(2) || rq[0]["maxAttempts"] != int64(3) {
		t.Errorf("requeued = %v", rq)
	}
	if st := env.rec.find("Run started"); len(st) != 1 || st[0]["attempt"] != int64(2) {
		t.Errorf("started = %v", st)
	}
}

func TestRecoverStopsAtMaxAttempts(t *testing.T) {
	spec := helperJob("env")
	spec.OnInterrupt, spec.MaxAttempts = InterruptRerun, 2
	path := filepath.Join(t.TempDir(), "kickd.db")
	id := interrupted(t, path, spec, "tired", 2)
	env := startDispatcher(t, path, spec)
	r, _ := env.store.GetRun(context.Background(), id)
	if r.Status != queue.StatusAbandoned || r.Reason != "max_attempts_reached" {
		t.Fatalf("run = %+v", r)
	}
	if _, ok, _ := env.store.RetryOf(context.Background(), id); ok {
		t.Error("no rerun expected past max_attempts")
	}
	if ab := env.rec.find("Interrupted run abandoned"); len(ab) != 1 || ab[0]["level"] != slog.LevelError {
		t.Errorf("abandoned = %v", ab)
	}
}

func TestRecoverAbandonsByDefault(t *testing.T) {
	spec := helperJob("env")
	path := filepath.Join(t.TempDir(), "kickd.db")
	id := interrupted(t, path, spec, "left", 1)
	gone := interrupted(t, path, Spec{Name: "removed"}, "gone", 1)
	env := startDispatcher(t, path, spec)
	if r, _ := env.store.GetRun(context.Background(), id); r.Status != queue.StatusAbandoned || r.Reason != "agent_crashed" {
		t.Errorf("abandon = %+v", r)
	}
	if r, _ := env.store.GetRun(context.Background(), gone); r.Status != queue.StatusAbandoned || r.Reason != "event_removed" {
		t.Errorf("removed = %+v", r)
	}
	if ab := env.rec.find("Interrupted run abandoned"); len(ab) != 2 || ab[0]["level"] != slog.LevelWarn {
		t.Errorf("abandoned lines = %v", ab)
	}
}

func TestQueuedRunsSurviveRestart(t *testing.T) {
	spec := helperJob("env")
	spec.Concurrency = PolicyQueue
	path := filepath.Join(t.TempDir(), "kickd.db")
	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(event.Event{RequestID: "before-restart", Name: spec.Name, Trigger: event.KindCron, TriggerID: "cron:@hourly"})
	id, _, err := s.Enqueue(context.Background(), queue.Run{RequestID: "before-restart", Event: spec.Name, Trigger: event.KindCron, TriggerID: "cron:@hourly", Payload: payload}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	env := startDispatcher(t, path, spec)
	waitRun(t, env.store, id, queue.StatusSucceeded)
}

func TestLogOutputFalseKeepsOutputOutOfTheQueue(t *testing.T) {
	spec := helperJob("exit")
	spec.LogOutput = false
	env := newDispatcher(t, spec)
	ev := fired(spec.Name)
	ev.RequestID = "quiet"
	res, err := env.d.Handler(spec.Name).RunSync(context.Background(), ev)
	if err != nil || !strings.Contains(res.Output, "failing on purpose") {
		t.Fatalf("a waiting caller still gets the output: res = %+v, err = %v", res, err)
	}
	if run := runOf(t, env.store, "quiet"); run.Output != "" {
		t.Errorf("stored output = %q, want none with log_output: false", run.Output)
	}
}
