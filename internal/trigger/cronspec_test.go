package trigger

import (
	"testing"
	"time"
)

func mustSchedule(t *testing.T, spec, tz string) Schedule {
	t.Helper()
	s, err := ParseSchedule(spec, tz)
	if err != nil {
		t.Fatalf("ParseSchedule(%q, %q): %v", spec, tz, err)
	}
	return s
}

// nexts returns the next n times after from.
func nexts(s Schedule, from time.Time, n int) []time.Time {
	var out []time.Time
	for i := 0; i < n; i++ {
		from = s.Next(from)
		out = append(out, from)
	}
	return out
}

func TestScheduleFields(t *testing.T) {
	utc := time.UTC
	from := time.Date(2026, 9, 24, 10, 7, 30, 0, utc) // a Thursday
	cases := []struct {
		spec string
		want []time.Time
	}{
		{"*/15 * * * *", []time.Time{time.Date(2026, 9, 24, 10, 15, 0, 0, utc), time.Date(2026, 9, 24, 10, 30, 0, 0, utc)}},
		{"0 9-17/4 * * *", []time.Time{time.Date(2026, 9, 24, 13, 0, 0, 0, utc), time.Date(2026, 9, 24, 17, 0, 0, 0, utc), time.Date(2026, 9, 25, 9, 0, 0, 0, utc)}},
		{"30 8 * * mon-fri", []time.Time{time.Date(2026, 9, 25, 8, 30, 0, 0, utc), time.Date(2026, 9, 28, 8, 30, 0, 0, utc)}},
		{"0 0 1 jan,jul *", []time.Time{time.Date(2027, 1, 1, 0, 0, 0, 0, utc), time.Date(2027, 7, 1, 0, 0, 0, 0, utc)}},
		// Both day fields restricted: the 1st of the month or any Sunday.
		{"0 12 1 * sun", []time.Time{time.Date(2026, 9, 27, 12, 0, 0, 0, utc), time.Date(2026, 10, 1, 12, 0, 0, 0, utc), time.Date(2026, 10, 4, 12, 0, 0, 0, utc)}},
		// "N/step" runs from N to the end of the field.
		{"50/5 10 * * *", []time.Time{time.Date(2026, 9, 24, 10, 50, 0, 0, utc), time.Date(2026, 9, 24, 10, 55, 0, 0, utc), time.Date(2026, 9, 25, 10, 50, 0, 0, utc)}},
		// Six fields: a leading seconds field.
		{"*/20 8 10 * * *", []time.Time{time.Date(2026, 9, 24, 10, 8, 0, 0, utc), time.Date(2026, 9, 24, 10, 8, 20, 0, utc)}},
		{"@hourly", []time.Time{time.Date(2026, 9, 24, 11, 0, 0, 0, utc)}},
		{"@daily", []time.Time{time.Date(2026, 9, 25, 0, 0, 0, 0, utc)}},
		{"@weekly", []time.Time{time.Date(2026, 9, 27, 0, 0, 0, 0, utc)}},
		{"@monthly", []time.Time{time.Date(2026, 10, 1, 0, 0, 0, 0, utc)}},
		{"@yearly", []time.Time{time.Date(2027, 1, 1, 0, 0, 0, 0, utc)}},
		// Leap days come every four years.
		{"0 0 29 feb *", []time.Time{time.Date(2028, 2, 29, 0, 0, 0, 0, utc), time.Date(2032, 2, 29, 0, 0, 0, 0, utc)}},
	}
	for _, c := range cases {
		got := nexts(mustSchedule(t, c.spec, "UTC"), from, len(c.want))
		for i := range c.want {
			if !got[i].Equal(c.want[i]) {
				t.Errorf("%q: next %d = %s, want %s", c.spec, i+1, got[i], c.want[i])
			}
		}
	}
}

func TestScheduleNeverReturnsZero(t *testing.T) {
	if next := mustSchedule(t, "0 0 30 feb *", "UTC").Next(time.Now()); !next.IsZero() {
		t.Errorf("February 30 never comes, got %s", next)
	}
}

func TestEveryRoundsToWholeSeconds(t *testing.T) {
	s := mustSchedule(t, "@every 90s", "")
	from := time.Date(2026, 9, 24, 10, 0, 0, 400_000_000, time.UTC)
	if got, want := s.Next(from), time.Date(2026, 9, 24, 10, 1, 30, 0, time.UTC); !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
	if got, want := mustSchedule(t, "@every 200ms", "").Next(from), time.Date(2026, 9, 24, 10, 0, 1, 0, time.UTC); !got.Equal(want) {
		t.Errorf("a delay below a second runs every second: got %s, want %s", got, want)
	}
}

// On the day daylight saving time starts, a time that the clock skips runs
// at the time the clock shows instead; on the day it ends, a time that the
// clock shows twice runs once.
func TestScheduleAcrossDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	s := mustSchedule(t, "30 2 * * *", "America/New_York")
	spring := s.Next(time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	if spring.In(ny).Day() != 8 || spring.In(ny).Hour() != 3 || spring.In(ny).Minute() != 30 {
		t.Errorf("2:30 on the day it does not exist = %s, want 3:30 EDT on March 8", spring.In(ny))
	}
	// Lord Howe Island moves its clock by half an hour.
	lh, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Skip(err)
	}
	gap := mustSchedule(t, "15 2 * * *", "Australia/Lord_Howe").Next(time.Date(2026, 10, 3, 12, 0, 0, 0, lh))
	if gap.In(lh).Day() != 4 || gap.In(lh).Hour() != 2 || gap.In(lh).Minute() != 45 {
		t.Errorf("2:15 on the day it does not exist = %s, want 2:45 on October 4", gap.In(lh))
	}
	s = mustSchedule(t, "30 1 * * *", "America/New_York")
	first := s.Next(time.Date(2026, 10, 31, 12, 0, 0, 0, ny))
	second := s.Next(first)
	if first.In(ny).Day() != 1 || second.In(ny).Day() != 2 {
		t.Errorf("1:30 on the day it repeats must run once: %s, then %s", first.In(ny), second.In(ny))
	}
}

func TestParseScheduleErrors(t *testing.T) {
	for _, spec := range []string{
		"60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 7",
		"5-1 * * * *", "*/0 * * * *", "*-5 * * * *", "a * * * *", "* * * foo *",
		"1,,2 * * * *", "@every", "@every soon", "@often", "* * * *", "* * * * * * *",
	} {
		if _, err := ParseSchedule(spec, ""); err == nil {
			t.Errorf("ParseSchedule(%q) should fail", spec)
		}
	}
}
