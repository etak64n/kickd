package trigger

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

// recordingHandler keeps the firings that it gets.
type recordingHandler struct {
	mu  sync.Mutex
	evs []event.Event
}

func (h *recordingHandler) Dispatch(ev event.Event) event.DispatchStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.evs = append(h.evs, ev)
	return event.Queued
}

func (h *recordingHandler) RunSync(context.Context, event.Event) (event.Result, error) {
	return event.Result{}, nil
}

func (h *recordingHandler) firings() []event.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]event.Event(nil), h.evs...)
}

// A sleep of the machine fires the events once, with its length; the time
// while the machine is awake does not.
func TestWakeWatcherFiresAfterSleep(t *testing.T) {
	var mu sync.Mutex
	var asleep time.Duration
	h := &recordingHandler{}
	w := &WakeWatcher{
		Events: map[string]event.Handler{"sync": h},
		Logger: slog.New(slog.DiscardHandler),
		Every:  10 * time.Millisecond,
		Slept: func() (time.Duration, error) {
			mu.Lock()
			defer mu.Unlock()
			return asleep, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	if f := h.firings(); len(f) != 0 {
		t.Fatalf("fired while awake: %+v", f)
	}
	mu.Lock()
	asleep += 90 * time.Minute
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.firings()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	f := h.firings()
	if len(f) != 1 || f[0].Name != "sync" || f[0].Trigger != event.KindWake || f[0].TriggerID != "wake" || f[0].Wake == nil || f[0].Wake.SleptSeconds != 5400 {
		t.Fatalf("firings = %+v", f)
	}
	if f[0].Wake.SleptAt.After(f[0].Time) {
		t.Errorf("slept at %s, after the wake at %s", f[0].Wake.SleptAt, f[0].Time)
	}
}

// The clocks of the OS agree while the machine is awake, so the watcher
// does not mistake the time that passes for a sleep.
func TestSleepClockHoldsWhileAwake(t *testing.T) {
	before, err := sleptSinceBoot()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	after, err := sleptSinceBoot()
	if err != nil {
		t.Fatal(err)
	}
	if d := after - before; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("the sleep clock moved %s in 2 seconds awake", d)
	}
	t.Logf("the machine has slept %s since it booted", after.Round(time.Second))
}
