package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
)

// endedRun adds a run of the event name that succeeded at finished, as an
// earlier agent leaves it in the database.
func endedRun(t *testing.T, s *queue.Store, name string, finished time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	id, _, err := s.Enqueue(ctx, queue.Run{RequestID: event.NewID(), Event: name, Trigger: event.KindManual, TriggerID: "manual",
		Payload: []byte(`{}`), Attempt: 1, CreatedAt: finished}, 0)
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

// When the agent starts, it deletes the runs that ended longer ago than
// database.retention, and keeps the runs that ended since.
func TestAgentDeletesTheRunsOlderThanTheRetentionWhenItStarts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "kickd.yaml")
	writeFile(t, cfgPath, "database:\n  retention: 24h\nevents:\n  - name: backup\n"+
		helperEvent("append", filepath.Join(dir, "backup.txt"), "HELPER_TEXT", "backed up")+"    triggers: [{type: manual}]\n")
	db := filepath.Join(dir, "kickd.db")
	before, err := queue.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	old := endedRun(t, before, "backup", time.Now().Add(-48*time.Hour))
	recent := endedRun(t, before, "backup", time.Now().Add(-time.Hour))
	before.Close()

	cancel, done := startAgent(t, cfgPath)
	defer stopAgent(t, cancel, done)
	store := openWhenAlive(t, db)
	ctx := context.Background()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_, err := store.GetRun(ctx, old)
		if errors.Is(err, queue.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run that ended two days ago is still there: %v", err)
		}
	}
	if _, err := store.GetRun(ctx, recent); err != nil {
		t.Errorf("the run that ended an hour ago: %v", err)
	}
}
