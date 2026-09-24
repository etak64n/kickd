// Package agent wires the configuration to triggers and the runner, and
// reloads the configuration when the file changes.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/etak64n/kickd/internal/config"
	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
	"github.com/etak64n/kickd/internal/queue"
	"github.com/etak64n/kickd/internal/runner"
	"github.com/etak64n/kickd/internal/trigger"
)

// Options configure Run.
type Options struct {
	ConfigPath string
	Version    string
	// ProcessID is the requestId of the lines that belong to no job run.
	// It changes on every start; empty generates one.
	ProcessID string
}

// SignalCause is the cancellation cause of a shutdown requested by a
// signal.
type SignalCause struct{ Signal os.Signal }

func (c SignalCause) Error() string { return "received " + logging.SignalName(c.Signal) }

// ErrServiceStop is the cancellation cause when the service manager stops
// the agent.
var ErrServiceStop = errors.New("service manager requested stop")

var errTriggerFailed = errors.New("trigger failed")

// Run starts the agent and blocks until ctx is done. The first
// configuration must load and start cleanly. Later, a configuration that
// fails to load is logged and the previous one stays in effect.
func Run(ctx context.Context, base *slog.Logger, opts Options) error {
	procID := opts.ProcessID
	if procID == "" {
		procID = event.NewID()
	}
	log := base.With("requestId", procID)
	started := time.Now()
	log.Info("Agent starting", "version", opts.Version, "pid", os.Getpid(), "file", opts.ConfigPath)

	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	store, err := openQueue(ctx, log, cfg.Queue.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	rn := runner.New(ctx, base, procID)
	disp := runner.NewDispatcher(rn, store, log, cfg.Queue.Retention)
	disp.Configure(specs(cfg))
	if err := disp.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	}
	loopCtx, stopLoop := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		disp.Loop(loopCtx, runner.AgentInfo(opts.Version, started))
	}()

	reload := make(chan string, 1)
	requestReload := func(reason string) {
		select {
		case reload <- reason:
		default:
		}
	}
	go watchConfig(ctx, log, opts.ConfigPath, requestReload)
	notifyHangup(ctx, func() { requestReload("signal") })

	shutdown := func() error {
		log.Info("Agent stopping", append([]any{"inFlightJobs", rn.InFlight()}, causeAttrs(context.Cause(ctx))...)...)
		rn.Wait()
		stopLoop()
		<-loopDone
		if err := store.AgentStopped(context.Background()); err != nil {
			log.Warn("Queue operation failed", "detail", "agent_stopped", "file", store.Path(), logging.Err(err))
		}
		logStopped(log, rn, started)
		return nil
	}

	first := true
	for {
		for _, w := range cfg.Warnings() {
			log.Warn("Config warning", "event", w.Event, "reason", w.Reason, "detail", w.Detail)
		}
		errCh, stop := startTriggers(ctx, cfg, disp, store, log)
		msg := "Config loaded"
		if !first {
			msg = "Config reloaded"
		}
		log.Info(msg, "file", cfg.Path, "eventCount", len(cfg.Events), "triggerCount", countTriggers(cfg))

		next, err := waitForChange(ctx, log, opts.ConfigPath, reload, errCh)
		switch {
		case ctx.Err() != nil:
			stop(context.Cause(ctx))
			return shutdown()
		case err != nil && first:
			stop(errTriggerFailed)
			stopLoop()
			<-loopDone
			return err
		case err != nil:
			stop(errTriggerFailed)
			log.Error("Trigger failed", logging.Err(err), "detail", "no triggers run until the config is saved again; queued runs and manual firings still run")
			next = waitForValidConfig(ctx, log, opts.ConfigPath, reload)
			if next == nil {
				return shutdown()
			}
		default:
			stop(event.ErrReload)
		}
		first = false
		if next.Queue.Path != cfg.Queue.Path {
			log.Warn("Queue path change needs a restart", "file", cfg.Queue.Path, "detail", next.Queue.Path)
			next.Queue.Path = cfg.Queue.Path
		}
		cfg = next
		disp.Configure(specs(cfg))
	}
}

// waitForChange blocks until shutdown, a trigger failure, or a reload
// that yields a valid configuration, which it returns. Invalid
// configurations are logged and the current one stays in effect.
func waitForChange(ctx context.Context, log *slog.Logger, path string, reload <-chan string, errCh <-chan error) (*config.Config, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, nil
		case err := <-errCh:
			return nil, err
		case reason := <-reload:
			log.Info("Config reload requested", "reason", reason)
			next, err := config.Load(path)
			if err != nil {
				log.Error("Config reload failed", "file", path, logging.Err(err), "detail", "the previous config stays in effect")
				continue
			}
			return next, nil
		}
	}
}

// waitForValidConfig waits, with no triggers running, for a reload that
// yields a valid configuration. It returns nil on shutdown.
func waitForValidConfig(ctx context.Context, log *slog.Logger, path string, reload <-chan string) *config.Config {
	next, _ := waitForChange(ctx, log, path, reload, nil)
	return next
}

// openQueue opens the queue database.
func openQueue(ctx context.Context, log *slog.Logger, path string) (*queue.Store, error) {
	store, err := queue.Open(path)
	if err != nil {
		return nil, err
	}
	queued, _, err := store.Counts(ctx)
	if err != nil {
		store.Close()
		return nil, err
	}
	log.Info("Queue opened", "file", store.Path(), "count", queued)
	return store, nil
}

func logStopped(log *slog.Logger, rn *runner.Runner, started time.Time) {
	st := rn.Stats()
	attrs := []any{
		"uptimeSec", int64(time.Since(started).Seconds()),
		"processed", st.Processed,
		"succeeded", st.Succeeded,
		"failed", st.Failed,
		"skipped", st.Skipped + st.Dropped,
	}
	if st.Canceled > 0 {
		attrs = append(attrs, "canceled", st.Canceled)
	}
	if st.Interrupted > 0 {
		attrs = append(attrs, "interrupted", st.Interrupted)
	}
	log.Info("Agent stopped", attrs...)
}

func causeAttrs(cause error) []any {
	var sc SignalCause
	switch {
	case errors.As(cause, &sc):
		return []any{"signal", logging.SignalName(sc.Signal)}
	case errors.Is(cause, ErrServiceStop):
		return []any{"reason", "service_stop"}
	}
	return []any{"reason", "context_canceled"}
}

// startTriggers builds the triggers of cfg and runs them until stop is
// called with a cause. Fatal trigger errors arrive on the returned channel.
func startTriggers(parent context.Context, cfg *config.Config, disp *runner.Dispatcher, state trigger.CronState, log *slog.Logger) (<-chan error, func(error)) {
	ctx, cancel := context.WithCancelCause(parent)
	cronSched := trigger.NewCronScheduler(log, state)
	var hook *trigger.WebhookServer
	type runnable struct {
		name string
		run  func(context.Context) error
	}
	var runnables []runnable

	for _, e := range cfg.Events {
		h := disp.Handler(e.Name)
		for k, tc := range e.Triggers {
			switch tc.Type {
			case config.TriggerCron:
				ct := trigger.CronTrigger{Event: e.Name, Index: k, Schedule: tc.Schedule, Timezone: tc.Timezone, Missed: tc.Missed}
				if err := cronSched.Add(ct, h); err != nil {
					log.Error("Trigger rejected", "event", e.Name, "trigger", event.KindCron, logging.Err(err))
				}
			case config.TriggerWebhook:
				if hook == nil {
					hook = trigger.NewWebhookServer(cfg.Webhook.Listen, cfg.Webhook.MaxBodyBytes, log)
				}
				route := trigger.WebhookRoute{
					Event:   e.Name,
					Path:    tc.Path,
					Methods: tc.Methods,
					Token:   tc.Token,
					Secret:  tc.Secret,
					Wait:    tc.Wait,
				}
				for _, p := range e.Params {
					route.Params = append(route.Params, p.Name)
					if p.Required {
						route.Required = append(route.Required, p.Name)
					}
				}
				if err := hook.Register(route, h); err != nil {
					log.Error("Trigger rejected", "event", e.Name, "trigger", event.KindWebhook, logging.Err(err))
				}
			case config.TriggerFile:
				fw := &trigger.FileWatcher{
					Event:     e.Name,
					Root:      tc.Path,
					Recursive: tc.Recursive,
					Include:   tc.Include,
					Exclude:   tc.Exclude,
					Ops:       tc.Changes,
					Debounce:  tc.Debounce,
					Logger:    log,
				}
				runnables = append(runnables, runnable{event.KindFile, func(ctx context.Context) error { return fw.Run(ctx, h) }})
			}
		}
	}
	if cronSched.Len() > 0 {
		runnables = append(runnables, runnable{event.KindCron, cronSched.Run})
	}
	if hook != nil {
		runnables = append(runnables, runnable{event.KindWebhook, hook.Run})
	}

	errCh := make(chan error, len(runnables)+1)
	var wg sync.WaitGroup
	for _, rb := range runnables {
		wg.Add(1)
		go func(rb runnable) {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					log.Error("Trigger panicked", "trigger", rb.name, logging.Panic(p, debug.Stack()))
					errCh <- fmt.Errorf("%s trigger panicked: %v", rb.name, p)
				}
			}()
			if err := rb.run(ctx); err != nil {
				errCh <- err
			}
		}(rb)
	}
	stop := func(cause error) {
		cancel(cause)
		wg.Wait()
	}
	return errCh, stop
}

// specs converts the events of cfg into runner specs.
func specs(cfg *config.Config) []runner.Spec {
	out := make([]runner.Spec, len(cfg.Events))
	for i, e := range cfg.Events {
		defaults := map[string]string{}
		for _, p := range e.Params {
			if p.Default != "" {
				defaults[p.Name] = p.Default
			}
		}
		out[i] = runner.Spec{
			Name:        e.Name,
			Command:     e.Command,
			Shell:       e.Shell,
			Workdir:     e.Workdir,
			Env:         e.Env,
			Timeout:     e.Timeout,
			Concurrency: e.Concurrency,
			OnInterrupt: e.OnInterrupt,
			MaxAttempts: e.MaxAttempts,
			Stdin:       e.Stdin,
			LogOutput:   e.LogsOutput(),
			Defaults:    defaults,
		}
	}
	return out
}

func countTriggers(cfg *config.Config) int {
	n := 0
	for _, e := range cfg.Events {
		n += len(e.Triggers)
	}
	return n
}

// watchConfig requests a reload whenever the config file changes.
func watchConfig(ctx context.Context, log *slog.Logger, path string, requestReload func(string)) {
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Warn("Config watch unavailable", "file", path, logging.Err(err), "detail", "saving the config file does not reload it")
		return
	}
	fw := &trigger.FileWatcher{
		Root:     filepath.Dir(abs),
		Include:  []string{filepath.Base(abs)},
		Ops:      []string{"create", "write", "rename", "remove"},
		Debounce: 500 * time.Millisecond,
		Logger:   log,
		Internal: true,
	}
	h := event.HandlerFunc(func(event.Event) { requestReload("file_changed") })
	if err := fw.Run(ctx, h); err != nil {
		log.Warn("Config watch unavailable", "file", abs, logging.Err(err), "detail", "saving the config file does not reload it")
	}
}
