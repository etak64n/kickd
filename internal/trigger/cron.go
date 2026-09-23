package trigger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
)

// CronParser accepts standard 5-field specs, an optional leading seconds
// field, and descriptors such as "@hourly" or "@every 10m".
var CronParser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// NormalizeSchedule prepends the time zone to spec when one is given and
// the spec does not already carry one.
func NormalizeSchedule(spec, tz string) string {
	spec = strings.TrimSpace(spec)
	if tz == "" || strings.HasPrefix(spec, "TZ=") || strings.HasPrefix(spec, "CRON_TZ=") {
		return spec
	}
	return "CRON_TZ=" + tz + " " + spec
}

// ParseSchedule validates a schedule and time zone.
func ParseSchedule(spec, tz string) (cron.Schedule, error) {
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, fmt.Errorf("timezone %q: %w", tz, err)
		}
	}
	return CronParser.Parse(NormalizeSchedule(spec, tz))
}

// CronScheduler runs every cron trigger of one configuration.
type CronScheduler struct {
	c       *cron.Cron
	logger  *slog.Logger
	entries map[cron.EntryID]cronEntry
}

type cronEntry struct {
	event, schedule, timezone string
}

// NewCronScheduler creates an empty scheduler.
func NewCronScheduler(logger *slog.Logger) *CronScheduler {
	cl := cronLogger{logger.With("trigger", event.KindCron)}
	return &CronScheduler{
		c: cron.New(
			cron.WithParser(CronParser),
			cron.WithLogger(cl),
			cron.WithChain(cron.Recover(cl)),
		),
		logger:  logger,
		entries: map[cron.EntryID]cronEntry{},
	}
}

// Add registers a schedule that fires the named event through h.
func (s *CronScheduler) Add(name, spec, tz string, h event.Handler) error {
	log := s.logger.With("trigger", event.KindCron, "event", name)
	id, err := s.c.AddFunc(NormalizeSchedule(spec, tz), func() {
		ev := event.Event{
			RequestID: event.NewID(),
			Name:      name,
			Trigger:   event.KindCron,
			TriggerID: "cron:" + spec,
			Time:      time.Now(),
			Cron:      &event.CronInfo{Schedule: spec},
		}
		status := h.Dispatch(ev)
		log.Log(context.Background(), logging.LevelTrace, "Cron schedule fired", "requestId", ev.RequestID, "schedule", spec, "dispatch", string(status))
	})
	if err != nil {
		return fmt.Errorf("cron %q: %w", spec, err)
	}
	s.entries[id] = cronEntry{event: name, schedule: spec, timezone: tz}
	return nil
}

// Len returns the number of registered schedules.
func (s *CronScheduler) Len() int { return len(s.entries) }

// Run starts the scheduler and blocks until ctx is done.
func (s *CronScheduler) Run(ctx context.Context) error {
	s.c.Start()
	for _, e := range s.c.Entries() {
		info := s.entries[e.ID]
		attrs := []any{"trigger", event.KindCron, "event", info.event, "schedule", info.schedule, "nextRunAt", e.Next}
		if info.timezone != "" {
			attrs = append(attrs, "timezone", info.timezone)
		}
		s.logger.Info("Cron schedule added", attrs...)
	}
	<-ctx.Done()
	<-s.c.Stop().Done()
	s.logger.Debug("Cron scheduler stopped", "trigger", event.KindCron, "count", len(s.entries))
	return nil
}

// cronLogger adapts slog to the logger interface of robfig/cron. The
// library's routine messages go to TRACE under one fixed message; its
// errors, including recovered panics, go to ERROR.
type cronLogger struct{ l *slog.Logger }

func (c cronLogger) Info(msg string, kv ...interface{}) {
	c.l.Log(context.Background(), logging.LevelTrace, "Cron scheduler event", append([]any{"detail", msg}, kv...)...)
}

func (c cronLogger) Error(err error, msg string, kv ...interface{}) {
	attrs := []any{"detail", msg, logging.Err(err)}
	stack := ""
	for i := 0; i+1 < len(kv); i += 2 {
		if k, _ := kv[i].(string); k == "stack" {
			stack, _ = kv[i+1].(string)
			continue
		}
		attrs = append(attrs, kv[i], kv[i+1])
	}
	if stack != "" {
		attrs = append(attrs, "stackTrace", logging.StackFrames([]byte(stack)))
	}
	c.l.Error("Cron scheduler error", attrs...)
}
