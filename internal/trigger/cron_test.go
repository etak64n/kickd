package trigger

import (
	"context"
	"log/slog"
	"os"
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

func TestNormalizeSchedule(t *testing.T) {
	if got := NormalizeSchedule(" 0 3 * * * ", "Asia/Tokyo"); got != "CRON_TZ=Asia/Tokyo 0 3 * * *" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeSchedule("TZ=UTC 0 3 * * *", "Asia/Tokyo"); got != "TZ=UTC 0 3 * * *" {
		t.Errorf("existing prefix must win, got %q", got)
	}
	if got := NormalizeSchedule("@hourly", ""); got != "@hourly" {
		t.Errorf("got %q", got)
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
	s := NewCronScheduler(logger)
	c := newCollector()
	if err := s.Add("tick", "@every 1s", "", c); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d", s.Len())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	ev := c.wait(t, 5*time.Second)
	if ev.Name != "tick" || ev.Trigger != event.KindCron || ev.Cron == nil || ev.Cron.Schedule != "@every 1s" {
		t.Errorf("event = %+v", ev)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
