package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/queue"
)

// A crash left the run running in the database, and kickd cancel asked to
// stop it before the agent started again: the run is canceled, not rerun.
func TestRecoverCancelsAnInterruptedRunThatKickdCancelAskedToStop(t *testing.T) {
	spec := helperJob("env")
	spec.OnInterrupt = InterruptRerun
	path := filepath.Join(t.TempDir(), "kickd.db")
	id := interrupted(t, path, spec, "asked", 1)
	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := s.RequestCancel(context.Background(), id); err != nil || !r.CancelRequested {
		t.Fatalf("RequestCancel = %+v, %v", r, err)
	}
	s.Close()

	env := startDispatcher(t, path, spec)
	r, _ := env.store.GetRun(context.Background(), id)
	if r.Status != queue.StatusCanceled || r.Reason != "canceled_by_user" || r.CancelRequested {
		t.Fatalf("run = %+v", r)
	}
	if _, ok, _ := env.store.RetryOf(context.Background(), id); ok {
		t.Error("the canceled run was rerun")
	}
	if c := env.rec.find("Interrupted run canceled"); len(c) != 1 || c[0]["detail"] != "agent_crashed" {
		t.Errorf("canceled lines = %v", c)
	}
}

// kickd cancel cancels a run that a clean stop interrupted at once, so the
// next start of the agent does not run it again.
func TestKickdCancelCancelsAnInterruptedRunAtOnce(t *testing.T) {
	spec := helperJob("env")
	spec.OnInterrupt = InterruptRerun
	path := filepath.Join(t.TempDir(), "kickd.db")
	id := interrupted(t, path, spec, "stopped", 1)
	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(context.Background(), id, queue.Finish{Status: queue.StatusInterrupted, Reason: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.RequestCancel(context.Background(), id)
	if err != nil || r.Status != queue.StatusCanceled || r.Reason != "canceled_by_user" || r.CancelRequested || r.StartedAt.IsZero() {
		t.Fatalf("RequestCancel = %+v, %v", r, err)
	}
	s.Close()

	env := startDispatcher(t, path, spec)
	if r, _ := env.store.GetRun(context.Background(), id); r.Status != queue.StatusCanceled {
		t.Fatalf("run = %+v", r)
	}
	if _, ok, _ := env.store.RetryOf(context.Background(), id); ok {
		t.Error("the canceled run was rerun")
	}
}

// A webhook caller that waits for a run gets an answer when kickd cancel,
// another process, cancels the run while it waits in the queue.
func TestAWaitingCallerGetsTheEndOfARunCanceledInTheQueue(t *testing.T) {
	spec := helperJob("sleep")
	spec.Concurrency = PolicyQueue
	path := filepath.Join(t.TempDir(), "kickd.db")
	env := startDispatcher(t, path, spec)
	h := env.d.Handler(spec.Name)
	h.Dispatch(fired(spec.Name))
	env.rec.waitFor(t, "Run started", 1, 10*time.Second)

	type answer struct {
		errText string
		err     error
	}
	done := make(chan answer, 1)
	waiting := fired(spec.Name)
	waiting.RequestID = "waits"
	go func() {
		res, err := h.RunSync(context.Background(), waiting)
		done <- answer{res.Error, err}
	}()
	var id int64
	for deadline := time.Now().Add(10 * time.Second); id == 0; {
		if runs, _ := env.store.RunsForRequest(context.Background(), "waits"); len(runs) > 0 {
			id = runs[0].ID
		} else if time.Now().After(deadline) {
			t.Fatal("the run of the waiting caller was not queued")
		} else {
			time.Sleep(20 * time.Millisecond)
		}
	}

	other, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if r, err := other.RequestCancel(context.Background(), id); err != nil || r.Status != queue.StatusCanceled {
		t.Fatalf("RequestCancel = %+v, %v", r, err)
	}
	select {
	case a := <-done:
		if a.err != nil || a.errText != queue.StatusCanceled {
			t.Fatalf("RunSync = %q, %v", a.errText, a.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the caller still waits after its run was canceled")
	}
}
