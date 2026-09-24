package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
	"github.com/etak64n/kickd/internal/trigger"
)

func waitForFile(t *testing.T, p string, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not contain %q (content %q, err %v)", p, want, b, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
}

func startAgent(t *testing.T, cfgPath string) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, quietLogger(), Options{ConfigPath: cfgPath, Version: "test"}) }()
	return cancel, done
}

func stopAgent(t *testing.T, cancel context.CancelFunc, done chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("agent did not stop")
	}
}

// openWhenAlive opens the agent's database once its heartbeat appears, the
// way "kickd status" finds a running agent.
func openWhenAlive(t *testing.T, db string) *queue.Store {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, err := queue.Open(db); err == nil {
			if a, ok, _ := s.AgentInfo(context.Background()); ok && a.Alive(15*time.Second) {
				t.Cleanup(func() { s.Close() })
				return s
			}
			s.Close()
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not write a heartbeat")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// kickEvent adds a firing the way "kickd event" does.
func kickEvent(t *testing.T, s *queue.Store, name string, data map[string]string) int64 {
	t.Helper()
	ev := event.Event{RequestID: event.NewID(), Name: name, Trigger: event.KindManual, TriggerID: "manual", Source: "test@host", Time: time.Now(), Data: data}
	payload, _ := json.Marshal(ev)
	id, _, err := s.Enqueue(context.Background(), queue.Run{RequestID: ev.RequestID, Event: name, Trigger: event.KindManual,
		TriggerID: "manual", Source: ev.Source, Payload: payload}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitStatus(t *testing.T, s *queue.Store, id int64, status string) queue.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		r, err := s.GetRun(context.Background(), id)
		if err == nil && r.Status == status {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %d: %q, want %q (%v)", id, r.Status, status, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunsTriggersAndReloads(t *testing.T) {
	dir := t.TempDir()
	watch := filepath.Join(dir, "in")
	if err := os.Mkdir(watch, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "kickd.yaml")
	outA := filepath.Join(dir, "a.txt")
	outB := filepath.Join(dir, "b.txt")
	base := "events:\n  - name: on-change\n" + helperEvent("append", outA, "HELPER_TEXT", "changed") +
		"    triggers:\n      - {type: file, path: in, include: ['*.md'], debounce: 100ms}\n"
	writeFile(t, cfgPath, base)
	cancel, done := startAgent(t, cfgPath)
	time.Sleep(300 * time.Millisecond)

	writeFile(t, filepath.Join(watch, "note.md"), "hi")
	waitForFile(t, filepath.Join(dir, "a.txt"), "changed", 10*time.Second)

	// Saving the config adds a cron event; the agent must pick it up.
	writeFile(t, cfgPath, base+"  - name: tick\n"+helperEvent("append", outB, "HELPER_TEXT", "tick")+"    triggers: [{type: cron, schedule: '@every 1s'}]\n")
	waitForFile(t, filepath.Join(dir, "b.txt"), "tick", 15*time.Second)
	stopAgent(t, cancel, done)
}

func TestAgentRejectsBrokenFirstConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "kickd.yaml")
	writeFile(t, cfgPath, "events: []\n")
	if err := Run(context.Background(), quietLogger(), Options{ConfigPath: cfgPath}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestAgentKeepsPreviousConfigOnBrokenReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "kickd.yaml")
	out := filepath.Join(dir, "tick.txt")
	writeFile(t, cfgPath, "events:\n  - name: tick\n"+helperEvent("append", out, "HELPER_TEXT", "tick")+"    triggers: [{type: cron, schedule: '@every 1s'}]\n")
	cancel, done := startAgent(t, cfgPath)
	waitForFile(t, out, "tick", 10*time.Second)

	writeFile(t, cfgPath, "events: [\n")
	time.Sleep(1500 * time.Millisecond)
	before, _ := os.ReadFile(out)
	time.Sleep(2500 * time.Millisecond)
	after, _ := os.ReadFile(out)
	if len(after) <= len(before) {
		t.Fatal("the previous config must keep running after a broken reload")
	}
	select {
	case err := <-done:
		t.Fatalf("agent exited: %v", err)
	default:
	}
	stopAgent(t, cancel, done)
}

func TestAgentRunsKickEvents(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "kickd.yaml")
	out := filepath.Join(dir, "deployed.txt")
	writeFile(t, cfgPath, "events:\n  - name: deploy\n    params: [{name: ref, default: main}]\n"+helperEvent("append-event", out))
	cancel, done := startAgent(t, cfgPath)
	store := openWhenAlive(t, filepath.Join(dir, "kickd.db"))

	id := kickEvent(t, store, "deploy", map[string]string{"ref": "v1.2"})
	waitForFile(t, filepath.Join(dir, "deployed.txt"), "ref=v1.2 event=deploy trigger=manual", 10*time.Second)
	if r := waitStatus(t, store, id, queue.StatusSucceeded); r.Source != "test@host" {
		t.Fatalf("run = %+v", r)
	}
	// A firing without the parameter gets its default.
	kickEvent(t, store, "deploy", nil)
	waitForFile(t, filepath.Join(dir, "deployed.txt"), "ref=main", 10*time.Second)

	stopAgent(t, cancel, done)
	if a, _, _ := store.AgentInfo(context.Background()); a.StoppedAt.IsZero() {
		t.Error("a clean stop must be recorded")
	}
}

// TestAgentRerunsAfterRestart stops kickd while a rerun event runs, starts
// it again, and checks that the second attempt runs to completion.
func TestAgentRerunsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "kickd.yaml")
	out := filepath.Join(dir, "attempts.txt")
	writeFile(t, cfgPath, "events:\n  - name: slow\n    on_interrupt: rerun\n    max_attempts: 2\n"+helperEvent("attempt", out))
	cancel, done := startAgent(t, cfgPath)
	store := openWhenAlive(t, filepath.Join(dir, "kickd.db"))
	first := kickEvent(t, store, "slow", nil)
	waitForFile(t, filepath.Join(dir, "attempts.txt"), "attempt=1", 10*time.Second)
	stopAgent(t, cancel, done)
	if r := waitStatus(t, store, first, queue.StatusInterrupted); r.Reason != "shutdown" {
		t.Fatalf("after stop = %+v", r)
	}

	cancel, done = startAgent(t, cfgPath)
	defer stopAgent(t, cancel, done)
	waitStatus(t, store, first, queue.StatusRetried)
	next, ok, err := store.RetryOf(context.Background(), first)
	if err != nil || !ok {
		t.Fatalf("no rerun: %v", err)
	}
	if r := waitStatus(t, store, next.ID, queue.StatusSucceeded); r.Attempt != 2 {
		t.Fatalf("rerun = %+v", r)
	}
	waitForFile(t, filepath.Join(dir, "attempts.txt"), "attempt=2", 5*time.Second)
}

// A cron trigger that came due while kickd was stopped runs once when kickd
// starts again with missed: run, and does not run with missed: skip.
func TestAgentHandlesCronMissedWhileStopped(t *testing.T) {
	// An hourly schedule 30 minutes away from now: its latest time is well
	// past the one minute that still counts as on time.
	minute := (time.Now().UTC().Minute() + 30) % 60
	schedule := fmt.Sprintf("%d * * * *", minute)
	for _, missed := range []string{"run", "skip"} {
		t.Run(missed, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "kickd.yaml")
			out := filepath.Join(dir, "cron.txt")
			writeFile(t, cfgPath, "events:\n  - name: hourly\n"+helperEvent("append-cron", out)+
				"    triggers:\n      - {type: cron, schedule: \""+schedule+"\", timezone: UTC, missed: "+missed+"}\n")
			store, err := queue.Open(filepath.Join(dir, "kickd.db"))
			if err != nil {
				t.Fatal(err)
			}
			key := trigger.CronStateKey(trigger.CronTrigger{Event: "hourly", Index: 0, Schedule: schedule, Timezone: "UTC"})
			if err := store.SetLastCron(context.Background(), key, time.Now().Add(-3*time.Hour)); err != nil {
				t.Fatal(err)
			}
			store.Close()

			cancel, done := startAgent(t, cfgPath)
			defer stopAgent(t, cancel, done)
			if missed == "run" {
				waitForFile(t, out, "missed=1 scheduledAt=", 10*time.Second)
				return
			}
			time.Sleep(2 * time.Second)
			if b, err := os.ReadFile(out); err == nil {
				t.Fatalf("missed: skip must not run a missed time, got %q", b)
			}
		})
	}
}
