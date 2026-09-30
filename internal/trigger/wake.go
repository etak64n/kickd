package trigger

import (
	"context"
	"log/slog"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

// MinSleep is the shortest sleep of the machine that fires wake triggers.
const MinSleep = time.Second

// WakeWatcher fires events when the machine wakes from sleep. It reads how
// long the machine has slept since it booted from two clocks of the OS: one
// that stops while the machine sleeps, and one that does not. A change of
// the wall clock moves neither, so it does not count as a sleep.
type WakeWatcher struct {
	// Events maps the name of each event with a wake trigger to its handler.
	Events map[string]event.Handler
	Logger *slog.Logger
	// Every is how often the watcher reads the clocks; one second when zero.
	Every time.Duration
	// Slept reads how long the machine has slept since it booted; the
	// clocks of the OS when nil.
	Slept func() (time.Duration, error)
}

// Run watches until ctx ends.
func (w *WakeWatcher) Run(ctx context.Context) error {
	slept, every := w.Slept, w.Every
	if slept == nil {
		slept = sleptSinceBoot
	}
	if every == 0 {
		every = time.Second
	}
	prev, err := slept()
	if err != nil {
		return err
	}
	prevAt := time.Now()
	w.Logger.Info("Wake watch started", "count", len(w.Events))
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		now := time.Now()
		cur, err := slept()
		if err != nil {
			w.Logger.Warn("Sleep clock unavailable", "detail", err.Error())
			continue
		}
		if d := cur - prev; d >= MinSleep {
			w.fire(now, prevAt, d)
		}
		prev, prevAt = cur, now
	}
}

// fire dispatches every event of the watcher for a sleep of d that began
// after the machine was last seen awake at awake.
func (w *WakeWatcher) fire(now, awake time.Time, d time.Duration) {
	w.Logger.Info("Wake detected", "sleptAt", awake.UTC().Format(time.RFC3339), "durationMs", d.Milliseconds(), "count", len(w.Events))
	for name, h := range w.Events {
		h.Dispatch(event.Event{
			RequestID: event.NewID(), Name: name, Trigger: event.KindWake, TriggerID: "wake", Time: now,
			Wake: &event.WakeInfo{SleptAt: awake.UTC(), SleptSeconds: int64(d.Round(time.Second) / time.Second)},
		})
	}
}
