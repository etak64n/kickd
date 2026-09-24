package trigger

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule gives the next time a cron trigger fires after a time.
type Schedule interface {
	Next(time.Time) time.Time
}

// NormalizeSchedule prepends the time zone to spec when one is given and
// the spec does not already carry one.
func NormalizeSchedule(spec, tz string) string {
	spec = strings.TrimSpace(spec)
	if tz == "" || strings.HasPrefix(spec, "TZ=") || strings.HasPrefix(spec, "CRON_TZ=") {
		return spec
	}
	return "CRON_TZ=" + tz + " " + spec
}

// ParseSchedule parses a cron schedule in the time zone tz, or in the
// local time zone when tz is empty. A schedule is five fields (minute,
// hour, day of month, month, day of week), six fields with a leading
// seconds field, or a descriptor: @yearly, @annually, @monthly, @weekly,
// @daily, @midnight, @hourly or "@every <duration>". A "CRON_TZ=zone" or
// "TZ=zone" prefix sets the time zone, too.
func ParseSchedule(spec, tz string) (Schedule, error) {
	spec = NormalizeSchedule(spec, tz)
	loc := time.Local
	if strings.HasPrefix(spec, "TZ=") || strings.HasPrefix(spec, "CRON_TZ=") {
		zone, rest, ok := strings.Cut(spec[strings.Index(spec, "=")+1:], " ")
		if !ok || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("no schedule after the time zone in %q", spec)
		}
		l, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("timezone %q: %w", zone, err)
		}
		loc, spec = l, strings.TrimSpace(rest)
	}
	if strings.HasPrefix(spec, "@") {
		return parseDescriptor(spec, loc)
	}
	fields := strings.Fields(spec)
	switch len(fields) {
	case 5:
		fields = append([]string{"0"}, fields...)
	case 6:
	default:
		return nil, fmt.Errorf("expected 5 or 6 fields, found %d in %q", len(fields), spec)
	}
	s := &calendar{loc: loc}
	var err error
	for i, f := range []struct {
		dst  *uint64
		star *bool
		r    fieldRange
	}{
		{&s.second, nil, secondRange},
		{&s.minute, nil, minuteRange},
		{&s.hour, nil, hourRange},
		{&s.dom, &s.domStar, domRange},
		{&s.month, nil, monthRange},
		{&s.dow, &s.dowStar, dowRange},
	} {
		var star bool
		if *f.dst, star, err = parseField(fields[i], f.r); err != nil {
			return nil, err
		}
		if f.star != nil {
			*f.star = star
		}
	}
	return s, nil
}

// fieldRange is the name and the allowed values of one field.
type fieldRange struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	secondRange = fieldRange{name: "seconds", min: 0, max: 59}
	minuteRange = fieldRange{name: "minutes", min: 0, max: 59}
	hourRange   = fieldRange{name: "hours", min: 0, max: 23}
	domRange    = fieldRange{name: "day of month", min: 1, max: 31}
	monthRange  = fieldRange{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}}
	dowRange = fieldRange{name: "day of week", min: 0, max: 6, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}}
)

// parseField parses a comma separated list of values, ranges and steps
// into a bit set. star reports a field that allows every value with "*"
// or "?"; the day fields need it to combine days of month and of week.
func parseField(expr string, r fieldRange) (bits uint64, star bool, err error) {
	for _, term := range strings.Split(expr, ",") {
		b, s, err := parseTerm(term, r)
		if err != nil {
			return 0, false, err
		}
		bits |= b
		star = star || s
	}
	return bits, star, nil
}

// parseTerm parses "*", "?", "N", "N-M", and any of them followed by
// "/step". "N/step" runs from N to the largest value of the field.
func parseTerm(term string, r fieldRange) (bits uint64, star bool, err error) {
	rangePart, stepPart, hasStep := strings.Cut(term, "/")
	lowPart, highPart, hasHigh := strings.Cut(rangePart, "-")
	var low, high int
	switch {
	case lowPart == "*" || lowPart == "?":
		if hasHigh {
			return 0, false, fmt.Errorf("%s: %q cannot have a range", r.name, term)
		}
		low, high, star = r.min, r.max, true
	default:
		if low, err = r.value(lowPart); err != nil {
			return 0, false, err
		}
		high = low
		if hasHigh {
			if high, err = r.value(highPart); err != nil {
				return 0, false, err
			}
		}
	}
	step := 1
	if hasStep {
		if step, err = strconv.Atoi(stepPart); err != nil || step <= 0 {
			return 0, false, fmt.Errorf("%s: %q has an invalid step", r.name, term)
		}
		if !hasHigh && !star {
			high = r.max
		}
		if step > 1 {
			star = false
		}
	}
	if low > high {
		return 0, false, fmt.Errorf("%s: %q starts after it ends", r.name, term)
	}
	for v := low; v <= high; v += step {
		bits |= 1 << uint(v)
	}
	return bits, star, nil
}

// value parses one number or name of the field.
func (r fieldRange) value(s string) (int, error) {
	if v, ok := r.names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", r.name, s)
	}
	if v < r.min || v > r.max {
		return 0, fmt.Errorf("%s: %d is outside %d-%d", r.name, v, r.min, r.max)
	}
	return v, nil
}

func parseDescriptor(spec string, loc *time.Location) (Schedule, error) {
	all := func(r fieldRange) uint64 {
		var b uint64
		for v := r.min; v <= r.max; v++ {
			b |= 1 << uint(v)
		}
		return b
	}
	s := &calendar{loc: loc, second: 1, minute: 1, hour: 1, dom: all(domRange), month: all(monthRange),
		dow: all(dowRange), domStar: true, dowStar: true}
	switch strings.ToLower(spec) {
	case "@yearly", "@annually":
		s.dom, s.month, s.domStar = 1<<1, 1<<1, false
	case "@monthly":
		s.dom, s.domStar = 1<<1, false
	case "@weekly":
		s.dow, s.dowStar = 1<<0, false
	case "@daily", "@midnight":
	case "@hourly":
		s.hour = all(hourRange)
	default:
		d, ok := strings.CutPrefix(spec, "@every ")
		if !ok {
			return nil, fmt.Errorf("unknown descriptor %q", spec)
		}
		delay, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", spec, err)
		}
		return every{delay: max(delay.Truncate(time.Second), time.Second)}, nil
	}
	return s, nil
}

// every fires at a fixed delay after the previous time, on whole seconds.
type every struct{ delay time.Duration }

func (e every) Next(t time.Time) time.Time {
	return t.Add(e.delay - time.Duration(t.Nanosecond()))
}

// calendar is a schedule of cron fields, as bit sets of allowed values.
type calendar struct {
	second, minute, hour, dom, month, dow uint64
	// domStar and dowStar mark day fields written as "*" or "?". When
	// both day fields are restricted, a day matches either of them, as in
	// the classic cron.
	domStar, dowStar bool
	loc              *time.Location
}

// maxSearchDays bounds the search for the next time to five years.
const maxSearchDays = 5*366 + 1

// Next returns the first scheduled time after t, or the zero time when
// the schedule has no time within five years.
func (s *calendar) Next(t time.Time) time.Time {
	local := t.In(s.loc)
	start := local.Add(time.Second - time.Duration(local.Nanosecond()))
	y, m, d := start.Date()
	for i := 0; i < maxSearchDays; i++ {
		// A date is counted at midnight UTC, where every day has 24 hours,
		// so a clock change at midnight cannot shift it.
		day := time.Date(y, m, d+i, 0, 0, 0, 0, time.UTC)
		if s.month&(1<<uint(day.Month())) == 0 || !s.dayMatches(day) {
			continue
		}
		if next, ok := s.firstOn(day, start); ok {
			return next.In(t.Location())
		}
	}
	return time.Time{}
}

func (s *calendar) dayMatches(day time.Time) bool {
	dom := s.dom&(1<<uint(day.Day())) != 0
	dow := s.dow&(1<<uint(day.Weekday())) != 0
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// firstOn returns the first scheduled time on the date of day, given at
// midnight UTC, that is not before start.
//
// A clock reading w after midnight comes at day+w-offset, with the UTC
// offset in effect then. Where daylight saving time starts, the clock
// skips some readings. A skipped reading runs at the moment it would have
// come with the offset from before the change, which the clock shows as a
// reading after the gap: 2:30 runs at 3:30 when the clock jumps from 2:00
// to 3:00. Where daylight saving time ends, the clock shows some readings
// twice, and such a reading runs at the first of them.
func (s *calendar) firstOn(day, start time.Time) (time.Time, bool) {
	lo, hi := s.offsets(day)
	// No reading before from comes at or after start, and a reading w
	// never comes before day+w-hi. With one offset in the day, readings
	// come in order, so the first one at or after start is the answer.
	from := max(start.Sub(day)+lo, 0)
	h0, m0, s0 := int(from/time.Hour), int(from/time.Minute%60), int(from/time.Second%60)
	var best time.Time
	for h := h0; h < 24; h++ {
		if s.hour&(1<<uint(h)) == 0 {
			continue
		}
		minFrom := 0
		if h == h0 {
			minFrom = m0
		}
		for mi := minFrom; mi < 60; mi++ {
			if s.minute&(1<<uint(mi)) == 0 {
				continue
			}
			secFrom := 0
			if h == h0 && mi == m0 {
				secFrom = s0
			}
			for se := secFrom; se < 60; se++ {
				if s.second&(1<<uint(se)) == 0 {
					continue
				}
				w := clockOf(h, mi, se)
				if !best.IsZero() && !day.Add(w-hi).Before(best) {
					return best, true
				}
				c, ok := s.moment(day, w, lo, hi)
				if !ok || c.Before(start) {
					continue
				}
				if lo == hi {
					return c, true
				}
				if best.IsZero() || c.Before(best) {
					best = c
				}
			}
		}
	}
	return best, !best.IsZero()
}

// moment returns the moment at which the clock on the date of day shows
// the reading w, given the smallest and the largest UTC offset of the day.
// It returns false when a skipped reading would come on another date.
func (s *calendar) moment(day time.Time, w, lo, hi time.Duration) (time.Time, bool) {
	// A reading that the clock shows twice comes first with the larger
	// offset.
	for _, off := range [2]time.Duration{hi, lo} {
		c := day.Add(w - off)
		if _, o := c.In(s.loc).Zone(); time.Duration(o)*time.Second == off {
			return c, true
		}
	}
	// The clock skips the reading. The offset from before the change is
	// the smaller one, and with it the reading comes after the gap.
	c := day.Add(w - lo)
	cy, cm, cd := c.In(s.loc).Date()
	y, m, d := day.Date()
	return c, cy == y && cm == m && cd == d
}

// offsets returns the smallest and the largest UTC offset in effect on the
// date of day. UTC offsets lie between -12h and +14h, so the date lies
// within day-14h and day+36h. A change on a neighboring date can widen the
// range, which costs only a longer search.
func (s *calendar) offsets(day time.Time) (lo, hi time.Duration) {
	_, a := day.Add(-14 * time.Hour).In(s.loc).Zone()
	_, b := day.Add(36 * time.Hour).In(s.loc).Zone()
	lo, hi = time.Duration(a)*time.Second, time.Duration(b)*time.Second
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

func clockOf(h, m, s int) time.Duration {
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}
