package trigger

import (
	"fmt"
	"slices"
	"strings"
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
	// Every skipped time runs, each at the moment it would have come, and
	// a time that the clock shows twice runs at the first of the two
	// instants, on either side of UTC.
	utc := func(mo time.Month, d, h, mi int) time.Time { return time.Date(2026, mo, d, h, mi, 0, 0, time.UTC) }
	for _, c := range []struct {
		spec, tz string
		from     time.Time
		want     []time.Time
	}{
		// 2:00, 2:20 and 2:40 EST run at 3:00, 3:20 and 3:40 EDT.
		{"*/20 2 * * *", "America/New_York", utc(3, 7, 17, 0), []time.Time{utc(3, 8, 7, 0), utc(3, 8, 7, 20), utc(3, 8, 7, 40), utc(3, 9, 6, 0)}},
		// 2:35 exists at UTC+11 and runs before the skipped 2:10, which
		// comes at 2:40.
		{"10,35 2 * * *", "Australia/Lord_Howe", utc(10, 3, 1, 30), []time.Time{utc(10, 3, 15, 35), utc(10, 3, 15, 40), utc(10, 4, 15, 10), utc(10, 4, 15, 35)}},
		// 1:30 EDT, then 1:30 EST on the next day.
		{"30 1 * * *", "America/New_York", utc(10, 31, 16, 0), []time.Time{utc(11, 1, 5, 30), utc(11, 2, 6, 30)}},
		// 2:30 CEST, then 2:30 CET on the next day.
		{"30 2 * * *", "Europe/Berlin", utc(10, 24, 10, 0), []time.Time{utc(10, 25, 0, 30), utc(10, 26, 1, 30)}},
		// 1:45 at UTC+11, then 1:45 at UTC+10:30 on the next day.
		{"45 1 * * *", "Australia/Lord_Howe", utc(4, 4, 1, 0), []time.Time{utc(4, 4, 14, 45), utc(4, 5, 15, 15)}},
		// From 1:30 CEST: 2:00 CEST, then 3:00 CET, without 2:00 CET.
		{"0 * * * *", "Europe/Berlin", utc(10, 24, 23, 30), []time.Time{utc(10, 25, 0, 0), utc(10, 25, 2, 0)}},
	} {
		if _, err := time.LoadLocation(c.tz); err != nil {
			t.Skip(err)
		}
		s := mustSchedule(t, c.spec, c.tz)
		got := c.from
		for i, want := range c.want {
			if got = s.Next(got); !got.Equal(want) {
				t.Errorf("%q in %s: run %d at %s, want %s", c.spec, c.tz, i+1, got.UTC(), want)
				break
			}
		}
	}
}

// TestScheduleOnClockChanges compares the times of schedules around every
// clock change of 2026 in zones with unusual changes with times worked out
// by brute force from the readings of the clock.
func TestScheduleOnClockChanges(t *testing.T) {
	zones := []string{
		"America/New_York", "Europe/Berlin", "Europe/Dublin", "Australia/Sydney",
		"America/St_Johns",    // UTC-3:30
		"Australia/Lord_Howe", // moves the clock by half an hour
		"Pacific/Chatham",     // UTC+12:45, changes at 2:45
		"Antarctica/Troll",    // moves the clock by two hours
		"America/Havana",      // changes at midnight
		"America/Santiago",    // changes at midnight, back into the previous date
		"Africa/Cairo",        // changes at midnight
	}
	specs := []string{
		"30 2 * * *", "*/20 2 * * *", "10,35 2 * * *", "* 2 * * *", "45 1 * * *",
		"0 * * * *", "*/15 * * * *", "0 */7 * * *", "0 0 * * *", "30 0 * * *",
		"*/10 23 * * *", "0 23,0,1 * * *", "0 0 * * 0", "*/30 * 2 * * *",
	}
	checked := 0
	for _, zone := range zones {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Logf("skipping %s: %v", zone, err)
			continue
		}
		for _, change := range clockChanges(loc, 2026) {
			for _, spec := range specs {
				s := mustSchedule(t, spec, zone).(*calendar)
				want := bruteForce(s, loc, change)
				if len(want) == 0 {
					continue
				}
				var got []time.Time
				for c := s.Next(want[0].Add(-time.Second)); !c.After(want[len(want)-1]); c = s.Next(c) {
					got = append(got, c)
				}
				if !slices.EqualFunc(got, want, time.Time.Equal) {
					t.Errorf("%q in %s around the change at %s:\n got %s\nwant %s",
						spec, zone, change.In(loc), readings(got, loc), readings(want, loc))
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Skip("no time zone data")
	}
}

// clockChanges returns the moments in year at which the UTC offset of loc
// changes.
func clockChanges(loc *time.Location, year int) []time.Time {
	offset := func(t time.Time) int {
		_, o := t.In(loc).Zone()
		return o
	}
	var out []time.Time
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	for a := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC); a.Before(end); a = a.Add(15 * time.Minute) {
		b := a.Add(15 * time.Minute)
		if offset(a) == offset(b) {
			continue
		}
		for b.Sub(a) > time.Second {
			mid := a.Add(b.Sub(a) / 2).Truncate(time.Second)
			if offset(mid) == offset(a) {
				a = mid
			} else {
				b = mid
			}
		}
		out = append(out, b)
	}
	return out
}

// bruteForce returns, in order, the times of s on the five local dates
// around a clock change. A reading of the clock runs at the first moment
// the clock shows it. A reading that the clock skips runs at the moment it
// would have come with the offset from before the change, if that moment
// is still on the same date.
func bruteForce(s *calendar, loc *time.Location, change time.Time) []time.Time {
	_, before := change.Add(-time.Second).In(loc).Zone()
	_, after := change.In(loc).Zone()
	y, m, d := change.In(loc).Date()
	var out []time.Time
	for i := -2; i <= 2; i++ {
		day := time.Date(y, m, d+i, 0, 0, 0, 0, time.UTC)
		if s.month&(1<<uint(day.Month())) == 0 || !s.dayMatches(day) {
			continue
		}
		for h := 0; h < 24; h++ {
			for mi := 0; mi < 60; mi++ {
				for se := 0; se < 60; se++ {
					if s.hour&(1<<uint(h)) == 0 || s.minute&(1<<uint(mi)) == 0 || s.second&(1<<uint(se)) == 0 {
						continue
					}
					w := time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(se)*time.Second
					var first time.Time
					for _, o := range []int{before, after} {
						c := day.Add(w - time.Duration(o)*time.Second)
						l := c.In(loc)
						ly, lm, ld := l.Date()
						lh, lmi, lse := l.Clock()
						if ly == day.Year() && lm == day.Month() && ld == day.Day() && lh == h && lmi == mi && lse == se &&
							(first.IsZero() || c.Before(first)) {
							first = c
						}
					}
					if first.IsZero() {
						c := day.Add(w - time.Duration(before)*time.Second)
						if ly, lm, ld := c.In(loc).Date(); ly != day.Year() || lm != day.Month() || ld != day.Day() {
							continue
						}
						first = c
					}
					out = append(out, first)
				}
			}
		}
	}
	slices.SortFunc(out, time.Time.Compare)
	return slices.CompactFunc(out, time.Time.Equal)
}

func readings(ts []time.Time, loc *time.Location) string {
	var b strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&b, "%s ", t.In(loc).Format("01-02 15:04:05 -0700"))
	}
	return b.String()
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
