package trigger

import (
	"context"
	"errors"
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

// sleepClock is a clock of the time that the machine has slept, which a
// test moves.
type sleepClock struct {
	mu    sync.Mutex
	slept time.Duration
	err   error
}

func (c *sleepClock) read() (time.Duration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slept, c.err
}

// sleep adds a sleep of d to the clock.
func (c *sleepClock) sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept += d
}

// fail makes the clock fail with err, or work again when err is nil.
func (c *sleepClock) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// watchWakes runs a WakeWatcher on clock for the events, which each get a
// handler, until the test ends.
func watchWakes(t *testing.T, clock *sleepClock, events ...string) map[string]*recordingHandler {
	t.Helper()
	handlers := map[string]*recordingHandler{}
	w := &WakeWatcher{Events: map[string]event.Handler{}, Logger: slog.New(slog.DiscardHandler), Every: 10 * time.Millisecond, Slept: clock.read}
	for _, e := range events {
		handlers[e] = &recordingHandler{}
		w.Events[e] = handlers[e]
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	time.Sleep(50 * time.Millisecond)
	return handlers
}

// waitFirings waits until h has n firings, and returns them after a moment
// in which more could arrive.
func waitFirings(t *testing.T, h *recordingHandler, n int) []event.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.firings()) < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return h.firings()
}

func TestWakeWatcherIgnoresASleepShorterThanMinSleep(t *testing.T) {
	clock := &sleepClock{}
	h := watchWakes(t, clock, "resync")["resync"]
	clock.sleep(MinSleep / 2)
	time.Sleep(200 * time.Millisecond)
	if f := h.firings(); len(f) != 0 {
		t.Errorf("a sleep of %s fired: %+v", MinSleep/2, f)
	}
}

func TestWakeWatcherFiresEveryEventWithAWakeTrigger(t *testing.T) {
	clock := &sleepClock{}
	hs := watchWakes(t, clock, "resync", "remount")
	clock.sleep(time.Hour)
	for name, h := range hs {
		if f := waitFirings(t, h, 1); len(f) != 1 || f[0].Name != name {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

func TestWakeWatcherFiresOnceForEachSleep(t *testing.T) {
	clock := &sleepClock{}
	h := watchWakes(t, clock, "resync")["resync"]
	clock.sleep(time.Hour)
	waitFirings(t, h, 1)
	clock.sleep(2 * time.Hour)
	f := waitFirings(t, h, 2)
	if len(f) != 2 || f[0].Wake.SleptSeconds != 3600 || f[1].Wake.SleptSeconds != 7200 {
		t.Errorf("firings: %+v", f)
	}
}

func TestWakeWatcherKeepsWatchingWhileTheSleepClockFails(t *testing.T) {
	clock := &sleepClock{}
	h := watchWakes(t, clock, "resync")["resync"]
	clock.fail(errors.New("clock unavailable"))
	time.Sleep(50 * time.Millisecond)
	clock.fail(nil)
	clock.sleep(time.Hour)
	if f := waitFirings(t, h, 1); len(f) != 1 {
		t.Errorf("firings after the clock failed: %+v", f)
	}
}

func TestWakeWatcherDoesNotStartWithoutASleepClock(t *testing.T) {
	clock := &sleepClock{err: errors.New("no clock")}
	w := &WakeWatcher{Events: map[string]event.Handler{"resync": &recordingHandler{}}, Logger: slog.New(slog.DiscardHandler), Slept: clock.read}
	if err := w.Run(context.Background()); err == nil {
		t.Error("Run started without a sleep clock")
	}
}

func TestWakeWatcherStopsWhenTheContextEnds(t *testing.T) {
	w := &WakeWatcher{Events: map[string]event.Handler{}, Logger: slog.New(slog.DiscardHandler), Every: 10 * time.Millisecond, Slept: (&sleepClock{}).read}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}
}
