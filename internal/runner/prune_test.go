package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
)

// setPruneEvery shortens the time between two prunes of the loop.
func setPruneEvery(t *testing.T, d time.Duration) {
	old := pruneEvery
	pruneEvery = d
	t.Cleanup(func() { pruneEvery = old })
}

// endedRun adds a run of the event name that succeeded at finished. The
// run waits for an hour while it is added, so that no loop starts it.
func endedRun(t *testing.T, s *queue.Store, name string, finished time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	id, _, err := s.Enqueue(ctx, queue.Run{RequestID: fmt.Sprintf("%s-%d", name, finished.UnixNano()), Event: name, Trigger: event.KindManual,
		TriggerID: "manual", Payload: []byte(`{}`), Attempt: 1, CreatedAt: finished, NotBefore: time.Now().Add(time.Hour)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.StartRun(ctx, id, finished); err != nil || !ok {
		t.Fatalf("StartRun: %v %v", ok, err)
	}
	if err := s.FinishRun(ctx, id, queue.Finish{Status: queue.StatusSucceeded, At: finished}); err != nil {
		t.Fatal(err)
	}
	return id
}

// The loop deletes the runs that ended longer ago than the retention when
// it starts, and again once an hour while it runs. The test shortens the
// hour to half a second.
func TestLoopDeletesOldRunsAgainEveryHour(t *testing.T) {
	setPruneEvery(t, 500*time.Millisecond)
	spec := helperJob("short")
	path := filepath.Join(t.TempDir(), "kickd.db")
	s, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	endedRun(t, s, spec.Name, time.Now().Add(-2*time.Hour))
	s.Close()
	env := startDispatcher(t, path, spec) // with a retention of an hour
	env.rec.waitFor(t, "Database pruned", 1, 10*time.Second)

	id := endedRun(t, env.store, spec.Name, time.Now().Add(-2*time.Hour))
	env.rec.waitFor(t, "Database pruned", 2, 10*time.Second)
	if _, err := env.store.GetRun(context.Background(), id); !errors.Is(err, queue.ErrNotFound) {
		t.Errorf("the run that ended two hours ago is still there: %v", err)
	}
}
