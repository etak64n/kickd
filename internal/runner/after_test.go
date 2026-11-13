package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
)

// An after trigger with the status dropped fires for a firing that its
// event dropped because 1000 runs of the event already waited, and passes
// that run, which has no exit code.
func TestAfterTriggerFiresForARunDroppedBecauseTheQueueWasFull(t *testing.T) {
	build := helperJob("sleep")
	build.Name, build.Concurrency = "build", PolicyQueue
	notify := helperJob("env")
	notify.Name = "notify"
	notify.After = []AfterTrigger{{Event: "build", Statuses: []string{queue.StatusDropped}}}
	env := newDispatcher(t, build, notify)
	h := env.d.Handler("build")
	running := fired("build")
	running.RequestID = "running"
	h.Dispatch(running)
	waitRun(t, env.store, runOf(t, env.store, "running").ID, queue.StatusRunning)
	enqueueQuietly(t, env.store, "build", MaxQueuedPerEvent)

	over := fired("build")
	over.RequestID = "over"
	if st := h.Dispatch(over); st != event.Dropped {
		t.Fatalf("a firing with %d runs waiting: %s, want dropped", MaxQueuedPerEvent, st)
	}
	dropped := runOf(t, env.store, "over")
	env.rec.waitFor(t, "After trigger fired", 1, 15*time.Second)
	runs, err := env.store.ListRuns(context.Background(), queue.Filter{Event: "notify"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("notify runs = %+v, %v", runs, err)
	}
	var ev event.Event
	if err := json.Unmarshal(runs[0].Payload, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.After == nil || ev.After.RunID != dropped.ID || ev.After.Status != queue.StatusDropped || ev.After.ExitCode != nil {
		t.Errorf("notify fired after %+v, want run %d, dropped, without an exit code", ev.After, dropped.ID)
	}
}
