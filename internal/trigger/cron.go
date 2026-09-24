package trigger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/etak64n/gocron"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
)

// What a cron trigger does with scheduled times that passed while the
// machine slept or kickd was stopped.
const (
	MissedRun  = "run"  // run once, right after the wake or the start
	MissedSkip = "skip" // wait for the next scheduled time
)

// MissedAfter is how late kickd may notice a scheduled time and still run
// it as on time. kickd checks the clock every second, so it notices a
// scheduled time this late only after the machine slept, or when kickd was
// not running.
const MissedAfter = time.Minute

// maxCronWait bounds the wait between two looks at the wall clock. Go
// timers measure time on a clock that stops while the machine sleeps, so
// one long timer would fire late after a wake.
const maxCronWait = time.Second

// ParseSchedule parses a cron schedule whose times are in the time zone
// tz, or in the local time zone when tz is empty. A "CRON_TZ=zone" or
// "TZ=zone" prefix on spec takes precedence over tz.
func ParseSchedule(spec, tz string) (gocron.Schedule, error) {
	loc := time.Local
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("timezone %q: %w", tz, err)
		}
		loc = l
	}
	return gocron.ParseInLocation(spec, loc)
}

// maxCatchUpSteps bounds the scheduled times counted after a long stop.
const maxCatchUpSteps = 1_000_000

// CronState keeps, for each cron trigger, when kickd last handled it, so
// that a start finds the scheduled times that passed while kickd was
// stopped.
type CronState interface {
	LastCron(ctx context.Context, key string) (time.Time, bool, error)
	SetLastCron(ctx context.Context, key string, at time.Time) error
}

// CronTrigger describes one cron trigger of an event.
type CronTrigger struct {
	Event string
	// Index is the position of the trigger among the triggers of the
	// event; it tells two triggers of one event apart in the state.
	Index    int
	Schedule string
	Timezone string
	Missed   string
}

// CronStateKey is the key under which the state of a cron trigger is kept.
func CronStateKey(t CronTrigger) string {
	return fmt.Sprintf("%s|%d|%s|%s", t.Event, t.Index, t.Timezone, strings.TrimSpace(t.Schedule))
}

// CronScheduler runs every cron trigger of one configuration.
type CronScheduler struct {
	logger  *slog.Logger
	state   CronState
	now     func() time.Time
	entries []*cronEntry
}

type cronEntry struct {
	CronTrigger
	key      string
	schedule gocron.Schedule
	handler  event.Handler
	// last is when kickd last handled the trigger; scheduled times after
	// it are due.
	last time.Time
}

// NewCronScheduler creates an empty scheduler. state may be nil, in which
// case times missed while kickd was stopped are not found.
func NewCronScheduler(logger *slog.Logger, state CronState) *CronScheduler {
	return &CronScheduler{logger: logger.With("trigger", event.KindCron), state: state, now: time.Now}
}

// Add registers a cron trigger that fires its event through h.
func (s *CronScheduler) Add(t CronTrigger, h event.Handler) error {
	sched, err := ParseSchedule(t.Schedule, t.Timezone)
	if err != nil {
		return fmt.Errorf("cron %q: %w", t.Schedule, err)
	}
	if t.Missed == "" {
		t.Missed = MissedRun
	}
	s.entries = append(s.entries, &cronEntry{CronTrigger: t, key: CronStateKey(t), schedule: sched, handler: h})
	return nil
}

// Len returns the number of registered schedules.
func (s *CronScheduler) Len() int { return len(s.entries) }

// Run fires the schedules until ctx is done. It looks at the wall clock at
// least once a second, so a scheduled time that passes while the machine
// sleeps is found right after the machine wakes.
func (s *CronScheduler) Run(ctx context.Context) error {
	now := s.now()
	for _, e := range s.entries {
		s.load(ctx, e, now)
		attrs := []any{"event", e.Event, "schedule", e.Schedule, "nextRunAt", e.schedule.Next(now)}
		if e.Timezone != "" {
			attrs = append(attrs, "timezone", e.Timezone)
		}
		s.logger.Info("Cron schedule added", attrs...)
	}
	for {
		s.check(ctx, s.now())
		timer := time.NewTimer(s.wait(s.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			s.logger.Debug("Cron scheduler stopped", "count", len(s.entries))
			return nil
		case <-timer.C:
		}
	}
}

// load reads when kickd last handled e. A trigger without a record starts
// from now, and the record is written so that a later stop is measured
// from here.
func (s *CronScheduler) load(ctx context.Context, e *cronEntry, now time.Time) {
	e.last = now
	if s.state == nil {
		return
	}
	last, ok, err := s.state.LastCron(ctx, e.key)
	switch {
	case err != nil:
		s.logger.Warn("Cron state unavailable", "event", e.Event, "schedule", e.Schedule, logging.Err(err),
			"detail", "scheduled times that passed while kickd was stopped are not checked")
	case ok && last.Before(now):
		e.last = last
	default:
		// No record, or a record in the future after the clock went back.
		s.save(ctx, e, now)
	}
}

func (s *CronScheduler) save(ctx context.Context, e *cronEntry, at time.Time) {
	if s.state == nil {
		return
	}
	if err := s.state.SetLastCron(ctx, e.key, at); err != nil {
		s.logger.Warn("Cron state not saved", "event", e.Event, "schedule", e.Schedule, logging.Err(err))
	}
}

// wait returns how long to sleep before the next look at the clock.
func (s *CronScheduler) wait(now time.Time) time.Duration {
	d := maxCronWait
	for _, e := range s.entries {
		next := e.schedule.Next(e.last)
		if next.IsZero() {
			continue
		}
		if w := next.Sub(now); w < d {
			d = w
		}
	}
	return max(d, time.Millisecond)
}

// check handles every trigger that has scheduled times up to now. All the
// scheduled times of one trigger that are due become one firing. A time
// noticed more than MissedAfter late passed while the machine slept or
// kickd was stopped, and the trigger's missed setting decides whether it
// runs.
func (s *CronScheduler) check(ctx context.Context, now time.Time) {
	for _, e := range s.entries {
		first := e.schedule.Next(e.last)
		if first.IsZero() || first.After(now) {
			continue
		}
		latest, missed := first, 0
		var firstMissed, lastMissed time.Time
		for t := first; !t.IsZero() && !t.After(now); t = e.schedule.Next(t) {
			latest = t
			if now.Sub(t) > MissedAfter {
				if missed == 0 {
					firstMissed = t
				}
				lastMissed = t
				missed++
			}
			if missed >= maxCatchUpSteps {
				break
			}
		}
		onTime := now.Sub(latest) <= MissedAfter
		e.last = now
		s.save(ctx, e, now)

		log := s.logger.With("event", e.Event)
		if missed > 0 {
			attrs := []any{"schedule", e.Schedule, "scheduledAt", lastMissed, "count", missed}
			if missed > 1 {
				attrs = append(attrs, "detail", "first missed at "+firstMissed.UTC().Format(time.RFC3339))
			}
			if e.Missed == MissedSkip {
				log.Info("Missed schedule skipped", append(attrs, "nextRunAt", e.schedule.Next(now))...)
			} else {
				log.Info("Missed schedule caught up", attrs...)
			}
		}
		switch {
		case onTime:
			s.fire(ctx, e, latest, false)
		case e.Missed != MissedSkip:
			s.fire(ctx, e, latest, true)
		}
	}
}

func (s *CronScheduler) fire(ctx context.Context, e *cronEntry, scheduled time.Time, missed bool) {
	ev := event.Event{
		RequestID: event.NewID(),
		Name:      e.Event,
		Trigger:   event.KindCron,
		TriggerID: "cron:" + e.Schedule,
		Time:      s.now(),
		Cron:      &event.CronInfo{Schedule: e.Schedule, ScheduledAt: scheduled.UTC(), Missed: missed},
	}
	status := e.handler.Dispatch(ev)
	s.logger.Log(ctx, logging.LevelTrace, "Cron schedule fired", "requestId", ev.RequestID, "event", e.Event,
		"schedule", e.Schedule, "scheduledAt", scheduled, "dispatch", string(status))
}
