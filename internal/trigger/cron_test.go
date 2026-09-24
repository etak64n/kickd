package trigger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

func TestParseSchedule(t *testing.T) {
	valid := []struct{ spec, tz string }{
		{"0 3 * * *", ""},
		{"*/5 * * * * *", ""},
		{"@every 10m", ""},
		{"@hourly", "Asia/Tokyo"},
		{"30 9 * * 1-5", "Asia/Tokyo"},
		{"CRON_TZ=UTC 0 0 * * *", ""},
		// Forms that gocron v0.2.0 added.
		{"0 0 L * *", "UTC"},
		{"0 9 * * mon#1", "Asia/Tokyo"},
		{"0 18 * * 5L", ""},
		{"0 0 * * 7", ""},
	}
	for _, c := range valid {
		if _, err := ParseSchedule(c.spec, c.tz); err != nil {
			t.Errorf("ParseSchedule(%q, %q) = %v", c.spec, c.tz, err)
		}
	}
	invalid := []struct{ spec, tz string }{
		{"every day", ""},
		{"", ""},
		{"0 3 * *", ""},
		{"@hourly", "Mars/Olympus"},
	}
	for _, c := range invalid {
		if _, err := ParseSchedule(c.spec, c.tz); err == nil {
			t.Errorf("ParseSchedule(%q, %q) should fail", c.spec, c.tz)
		}
	}
}

func TestScheduleNextInTimezone(t *testing.T) {
	s, err := ParseSchedule("0 9 * * *", "Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(from)
	if next.UTC() != time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) && next.UTC() != time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC) {
		t.Errorf("next = %s, want 09:00 JST (00:00 UTC)", next.UTC())
	}
}

func TestCronSchedulerFires(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := NewCronScheduler(logger, nil)
	c := newCollector()
	if err := s.Add(CronTrigger{Event: "tick", Schedule: "@every 1s"}, c); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d", s.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	ev := c.wait(t, 5*time.Second)
	if ev.Name != "tick" || ev.Trigger != event.KindCron || ev.Cron == nil || ev.Cron.Schedule != "@every 1s" || ev.Cron.Missed {
		t.Errorf("event = %+v", ev)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// memState keeps cron state in memory.
type memState struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newMemState() *memState { return &memState{last: map[string]time.Time{}} }

func (m *memState) LastCron(_ context.Context, key string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.last[key]
	return at, ok, nil
}

func (m *memState) SetLastCron(_ context.Context, key string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last[key] = at
	return nil
}

var (
	day = time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	at3 = day.Add(3 * time.Hour) // the scheduled time of "0 3 * * *" today
)

// daily returns a scheduler with a trigger at 03:00 UTC every day, loaded
// at now with last as the time kickd last handled it (none when zero).
func daily(t *testing.T, missed string, last, now time.Time) (*CronScheduler, *collector, *memState, CronTrigger) {
	t.Helper()
	state := newMemState()
	ct := CronTrigger{Event: "backup", Schedule: "0 3 * * *", Timezone: "UTC", Missed: missed}
	if !last.IsZero() {
		state.last[CronStateKey(ct)] = last
	}
	s := NewCronScheduler(slog.New(slog.NewTextHandler(io.Discard, nil)), state)
	s.now = func() time.Time { return now }
	c := newCollector()
	if err := s.Add(ct, c); err != nil {
		t.Fatal(err)
	}
	s.load(context.Background(), s.entries[0], now)
	return s, c, state, ct
}

// fired returns the events dispatched so far.
func fired(c *collector) []event.Event {
	var out []event.Event
	for {
		select {
		case ev := <-c.ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestCronRunsOnTime(t *testing.T) {
	now := at3.Add(500 * time.Millisecond)
	s, c, state, ct := daily(t, MissedRun, at3.Add(-time.Second), now)
	s.check(context.Background(), now)
	evs := fired(c)
	if len(evs) != 1 || evs[0].Cron.Missed || !evs[0].Cron.ScheduledAt.Equal(at3) {
		t.Fatalf("events = %+v", evs)
	}
	if !state.last[CronStateKey(ct)].Equal(now) {
		t.Errorf("state = %v, want %v", state.last[CronStateKey(ct)], now)
	}
}

func TestCronLateWithinAMinuteIsOnTime(t *testing.T) {
	now := at3.Add(30 * time.Second)
	s, c, _, _ := daily(t, MissedSkip, at3.Add(-time.Minute), now)
	s.check(context.Background(), now)
	if evs := fired(c); len(evs) != 1 || evs[0].Cron.Missed {
		t.Fatalf("a run 30 seconds late is on time, even with skip: %+v", evs)
	}
}

// The machine slept, or kickd was stopped, from before 03:00 to 09:00.
func TestCronMissedRunCatchesUpOnce(t *testing.T) {
	now := day.Add(9 * time.Hour)
	s, c, _, _ := daily(t, MissedRun, at3.AddDate(0, 0, -1).Add(400*time.Millisecond), now)
	s.check(context.Background(), now)
	evs := fired(c)
	if len(evs) != 1 || !evs[0].Cron.Missed || !evs[0].Cron.ScheduledAt.Equal(at3) {
		t.Fatalf("events = %+v", evs)
	}
	s.check(context.Background(), now.Add(time.Second))
	if evs := fired(c); len(evs) != 0 {
		t.Fatalf("a second check must not run again: %+v", evs)
	}
}

func TestCronMissedSkipWaitsForTheNextTime(t *testing.T) {
	now := day.Add(9 * time.Hour)
	s, c, _, _ := daily(t, MissedSkip, at3.AddDate(0, 0, -1).Add(400*time.Millisecond), now)
	s.check(context.Background(), now)
	if evs := fired(c); len(evs) != 0 {
		t.Fatalf("skip must not run a missed time: %+v", evs)
	}
	tomorrow := at3.AddDate(0, 0, 1).Add(300 * time.Millisecond)
	s.check(context.Background(), tomorrow)
	if evs := fired(c); len(evs) != 1 || evs[0].Cron.Missed {
		t.Fatalf("the next scheduled time must run on time: %+v", evs)
	}
}

// Three days without a check make one run, for the latest scheduled time.
func TestCronCoalescesMissedTimes(t *testing.T) {
	now := day.Add(9 * time.Hour)
	s, c, _, _ := daily(t, MissedRun, at3.AddDate(0, 0, -3).Add(400*time.Millisecond), now)
	s.check(context.Background(), now)
	if evs := fired(c); len(evs) != 1 || !evs[0].Cron.ScheduledAt.Equal(at3) {
		t.Fatalf("events = %+v", evs)
	}
}

// Starting at a scheduled time after a long stop runs that time on time;
// with skip, only the earlier times are skipped.
func TestCronDueAtStartRunsOnTimeWithSkip(t *testing.T) {
	now := at3.Add(10 * time.Second)
	s, c, _, _ := daily(t, MissedSkip, at3.AddDate(0, 0, -2), now)
	s.check(context.Background(), now)
	if evs := fired(c); len(evs) != 1 || evs[0].Cron.Missed || !evs[0].Cron.ScheduledAt.Equal(at3) {
		t.Fatalf("events = %+v", evs)
	}
}

// A trigger without a record starts from now, and does not run for times
// before it existed.
func TestCronFirstStartDoesNotCatchUp(t *testing.T) {
	now := day.Add(9 * time.Hour)
	s, c, state, ct := daily(t, MissedRun, time.Time{}, now)
	s.check(context.Background(), now)
	if evs := fired(c); len(evs) != 0 {
		t.Fatalf("events = %+v", evs)
	}
	if !state.last[CronStateKey(ct)].Equal(now) {
		t.Errorf("the first start must record now, got %v", state.last[CronStateKey(ct)])
	}
}

func TestCronWaitLooksAtTheClockEverySecond(t *testing.T) {
	now := day.Add(9 * time.Hour)
	s, _, _, _ := daily(t, MissedRun, now, now)
	if w := s.wait(now); w > time.Second || w <= 0 {
		t.Fatalf("wait = %s, want at most 1s before the next look at the clock", w)
	}
	if w := s.wait(at3.AddDate(0, 0, 1).Add(-200 * time.Millisecond)); w > 250*time.Millisecond {
		t.Errorf("wait = %s, want the time left until the schedule", w)
	}
}

// Run makes up for the times missed while kickd was stopped as soon as it
// starts.
func TestCronRunCatchesUpAtStart(t *testing.T) {
	state := newMemState()
	ct := CronTrigger{Event: "hourly", Schedule: "@hourly"}
	state.last[CronStateKey(ct)] = time.Now().Add(-3 * time.Hour)
	s := NewCronScheduler(slog.New(slog.NewTextHandler(io.Discard, nil)), state)
	c := newCollector()
	if err := s.Add(ct, c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	ev := c.wait(t, 3*time.Second)
	if ev.Cron == nil || ev.Cron.ScheduledAt.IsZero() {
		t.Errorf("event = %+v", ev)
	}
	c.none(t, 1500*time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
