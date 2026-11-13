// Package agent wires the configuration to triggers and the runner, and
// reloads the configuration when the file changes.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
// fails to load or whose webhook address cannot be bound is logged, and
// the previous one stays in effect.
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
	// Two agents on one database would both run every firing, and each
	// would take the running runs of the other for runs that a crash cut
	// off, so the second one stops here.
	release, err := queue.LockAgent(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer release()
	store, err := openQueue(ctx, log, cfg.Database.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	// The commands run until ctx ends, or until the agent gives up
	// because a trigger failed.
	runCtx, endRuns := context.WithCancelCause(ctx)
	defer endRuns(nil)
	rn := runner.New(runCtx, base, procID)
	disp := runner.NewDispatcher(rn, store, log, cfg.Database.Retention)
	disp.Configure(specs(cfg))
	if err := disp.Recover(ctx); err != nil {
		return fmt.Errorf("recover interrupted runs: %w", err)
	}

	reload := make(chan string, 1)
	requestReload := func(reason string) {
		select {
		case reload <- reason:
		default:
		}
	}
	go watchConfig(ctx, log, opts.ConfigPath, requestReload)
	notifyHangup(ctx, func() { requestReload("signal") })

	// The triggers start before the agent consumes the queue, so that a
	// trigger that cannot start, such as a webhook server whose address is
	// in use, stops the agent before it starts any command.
	trig, err := startTriggers(ctx, cfg, disp, store, log, nil)
	if err != nil {
		return err
	}

	loopCtx, stopLoop := context.WithCancel(context.Background())
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		disp.Loop(loopCtx, runner.AgentInfo(opts.Version, started))
	}()

	shutdown := func(cause error) {
		log.Info("Agent stopping", append([]any{"inFlightJobs", rn.InFlight()}, causeAttrs(cause)...)...)
		rn.Wait()
		stopLoop()
		<-loopDone
		if err := store.AgentStopped(context.Background()); err != nil {
			log.Warn("Database operation failed", "detail", "agent_stopped", "file", store.Path(), logging.Err(err))
		}
		logStopped(log, rn, started)
	}

	first := true
	for {
		for _, w := range cfg.Warnings() {
			log.Warn("Config warning", "event", w.Event, "reason", w.Reason, "detail", w.Detail)
		}
		msg := "Config loaded"
		if !first {
			msg = "Config reloaded"
		}
		log.Info(msg, "file", cfg.Path, "eventCount", len(cfg.Events), "triggerCount", countTriggers(cfg))
		if first {
			fireStartup(cfg, disp, log)
		}

		linksCtx, stopLinks := context.WithCancel(ctx)
		watchLinks(linksCtx, log, cfg, requestReload)
		next, ln, err := waitForReload(ctx, log, opts.ConfigPath, reload, trig.errs, cfg)
		stopLinks()
		switch {
		case ctx.Err() != nil:
			if ln != nil {
				ln.Close()
			}
			trig.stop(context.Cause(ctx))
			shutdown(context.Cause(ctx))
			return nil
		case err != nil && first:
			// A trigger of the first config failed after it started. The
			// agent stops as it does at a clean stop, so the runs that
			// started are recorded as interrupted, and on_interrupt decides
			// at the next start what becomes of them.
			trig.stop(errTriggerFailed)
			endRuns(errTriggerFailed)
			shutdown(errTriggerFailed)
			return err
		case err != nil:
			trig.stop(errTriggerFailed)
			log.Error("Trigger failed", logging.Err(err), "detail", "no triggers run until the config is saved again; queued runs and manual firings still run")
			next, ln, _ = waitForReload(ctx, log, opts.ConfigPath, reload, nil, nil)
			if next == nil {
				shutdown(context.Cause(ctx))
				return nil
			}
		default:
			trig.stop(event.ErrReload)
		}
		first = false
		if next.Database.Path != cfg.Database.Path {
			log.Warn("Database path change needs a restart", "file", cfg.Database.Path, "detail", next.Database.Path)
			next.Database.Path = cfg.Database.Path
		}
		cfg = next
		disp.Configure(specs(cfg))
		if trig, err = startTriggers(ctx, cfg, disp, store, log, ln); err != nil {
			// The address of the webhook server stayed the same, and
			// another program took it while the triggers restarted.
			trig = failedTriggers(err)
		}
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
		case <-reload:
			next, err := config.Load(path)
			if err != nil {
				log.Error("Config reload failed", "file", path, logging.Err(err), "detail", "the previous config stays in effect")
				continue
			}
			return next, nil
		}
	}
}

// waitForReload waits, as waitForChange does, for a valid configuration
// that can replace cur, the configuration whose triggers run, or nil when
// none run. A configuration whose webhook server needs an address that
// cur does not hold gets that address bound first, and the listener is
// returned with it; an address in use keeps cur in effect.
func waitForReload(ctx context.Context, log *slog.Logger, path string, reload <-chan string, errCh <-chan error, cur *config.Config) (*config.Config, net.Listener, error) {
	for {
		next, err := waitForChange(ctx, log, path, reload, errCh)
		if next == nil || err != nil {
			return nil, nil, err
		}
		addr, ok := webhookAddr(next)
		if !ok {
			return next, nil, nil
		}
		if held, ok := webhookAddr(cur); ok && held == addr {
			return next, nil, nil
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Error("Config reload failed", "file", path, logging.Err(fmt.Errorf("webhook listen: %w", err)), "detail", "the previous config stays in effect")
			continue
		}
		return next, ln, nil
	}
}

// webhookAddr returns the address that the webhook server of cfg listens
// on, and false when cfg has no webhook server.
func webhookAddr(cfg *config.Config) (string, bool) {
	if cfg == nil || !cfg.Webhook.IsEnabled() {
		return "", false
	}
	for _, e := range cfg.Events {
		for _, t := range e.Triggers {
			if t.Type == config.TriggerWebhook {
				return cfg.Webhook.Listen, true
			}
		}
	}
	return "", false
}

// openQueue opens the database.
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
	log.Info("Database opened", "file", store.Path(), "count", queued)
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
	case errors.Is(cause, errTriggerFailed):
		return []any{"reason", "trigger_failed"}
	}
	return []any{"reason", "context_canceled"}
}

// triggers are the running triggers of one configuration. Fatal trigger
// errors arrive on errs, and stop ends the triggers with a cause.
type triggers struct {
	errs <-chan error
	stop func(error)
}

// failedTriggers stands for triggers that could not start: err is waiting
// on errs, and nothing runs.
func failedTriggers(err error) *triggers {
	errs := make(chan error, 1)
	errs <- err
	return &triggers{errs: errs, stop: func(error) {}}
}

// startTriggers builds the triggers of cfg and runs them until stop is
// called. The webhook server listens on ln, or binds its address here,
// so an address in use is an error before any trigger runs.
func startTriggers(parent context.Context, cfg *config.Config, disp *runner.Dispatcher, state trigger.CronState, log *slog.Logger, ln net.Listener) (*triggers, error) {
	ctx, cancel := context.WithCancelCause(parent)
	cronSched := trigger.NewCronScheduler(log, state)
	var hook *trigger.WebhookServer
	disabledHooks := 0
	wakes := map[string]event.Handler{}
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
				if !cfg.Webhook.IsEnabled() {
					disabledHooks++
					continue
				}
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
			case config.TriggerWake:
				wakes[e.Name] = h
			}
		}
	}
	if len(wakes) > 0 {
		ww := &trigger.WakeWatcher{Events: wakes, Logger: log}
		runnables = append(runnables, runnable{event.KindWake, ww.Run})
	}
	if cronSched.Len() > 0 {
		runnables = append(runnables, runnable{event.KindCron, cronSched.Run})
	}
	if disabledHooks > 0 {
		log.Info("Webhook triggers disabled", "count", disabledHooks, "detail", "webhook.enabled is false, so the HTTP server does not start")
	}
	if hook != nil {
		if ln != nil {
			hook.SetListener(ln)
		} else if err := hook.Listen(); err != nil {
			cancel(err)
			return nil, err
		}
		runnables = append(runnables, runnable{event.KindWebhook, hook.Run})
	} else if ln != nil {
		ln.Close()
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
	return &triggers{errs: errCh, stop: stop}, nil
}

// specs converts the events of cfg into runner specs.
func specs(cfg *config.Config) []runner.Spec {
	out := make([]runner.Spec, len(cfg.Events))
	for i, e := range cfg.Events {
		// Every declared parameter reaches the command, empty without a value.
		defaults := map[string]string{}
		for _, p := range e.Params {
			defaults[p.Name] = p.Default
		}
		var after []runner.AfterTrigger
		for _, t := range e.Triggers {
			if t.Type == config.TriggerAfter {
				after = append(after, runner.AfterTrigger{Event: t.Event, Statuses: t.Status})
			}
		}
		out[i] = runner.Spec{
			Name:            e.Name,
			Command:         e.Command,
			Shell:           e.Shell,
			Workdir:         e.Workdir,
			Env:             e.Env,
			BaseEnv:         cfg.Env,
			User:            e.User,
			Timeout:         e.Timeout,
			StopCommand:     e.StopCommand,
			StopShell:       e.StopShell,
			StopTimeout:     e.StopTimeout,
			Concurrency:     e.Concurrency,
			Restart:         e.Restart,
			RestartDelay:    e.RestartDelay,
			RestartMaxDelay: e.RestartMaxDelay,
			OnInterrupt:     e.OnInterrupt,
			MaxAttempts:     e.MaxAttempts,
			Stdin:           e.Stdin,
			LogOutput:       e.LogsOutput(),
			Output:          outputOf(e),
			Defaults:        defaults,
			After:           after,
		}
	}
	return out
}

// outputOf is the output file of an event for the runner, or nil.
func outputOf(e config.Event) *runner.Output {
	if e.Output == nil {
		return nil
	}
	return &runner.Output{Path: e.Output.Path, MaxBytes: int64(e.Output.MaxSizeMB) << 20, MaxBackups: e.Output.MaxBackups}
}

// fireStartup fires the events with a startup trigger. The agent calls it
// once, when it starts; a reload does not fire them.
func fireStartup(cfg *config.Config, disp *runner.Dispatcher, log *slog.Logger) {
	now := time.Now()
	for _, e := range cfg.Events {
		for _, t := range e.Triggers {
			if t.Type != config.TriggerStartup {
				continue
			}
			ev := event.Event{RequestID: event.NewID(), Trigger: event.KindStartup, TriggerID: "startup", Time: now}
			disp.Handler(e.Name).Dispatch(ev)
			log.Info("Startup trigger fired", "requestId", ev.RequestID, "event", e.Name)
		}
	}
}

func countTriggers(cfg *config.Config) int {
	n := 0
	for _, e := range cfg.Events {
		n += len(e.Triggers)
	}
	return n
}

// watchConfig requests a reload whenever the config file, or another YAML
// file in its directory, changes.
func watchConfig(ctx context.Context, log *slog.Logger, path string, requestReload func(string)) {
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Warn("Config watch unavailable", "file", path, logging.Err(err), "detail", "saving the config file does not reload it")
		return
	}
	// The other YAML files of the directory can add events, so a change to
	// any of them reloads the config too.
	fw := &trigger.FileWatcher{
		Root:     filepath.Dir(abs),
		Include:  []string{filepath.Base(abs), "*.yaml", "*.yml"},
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

// watchLinks watches the files that the config file and the files of
// events point to when they are symbolic links, such as an events file
// linked from a repository. The watch of the config directory sees a
// change to a link, but not to the file it points to, which a pull of the
// repository changes. Each watch lasts until ctx is done.
func watchLinks(ctx context.Context, log *slog.Logger, cfg *config.Config, requestReload func(string)) {
	configDir, err := filepath.EvalSymlinks(cfg.Dir)
	if err != nil {
		configDir = cfg.Dir
	}
	targets := map[string][]string{} // directory -> names of the targets in it
	for _, f := range append(append([]string{cfg.Path}, cfg.EventFiles...), cfg.Skipped...) {
		st, err := os.Lstat(f)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := filepath.EvalSymlinks(f)
		if err != nil {
			continue
		}
		dir, name := filepath.Split(target)
		dir = filepath.Clean(dir)
		if dir == configDir {
			continue // the watch of the config directory sees it
		}
		targets[dir] = append(targets[dir], name)
	}
	h := event.HandlerFunc(func(event.Event) { requestReload("linked_file_changed") })
	for dir, names := range targets {
		fw := &trigger.FileWatcher{
			Root:     dir,
			Include:  names,
			Ops:      []string{"create", "write", "rename", "remove"},
			Debounce: 500 * time.Millisecond,
			Logger:   log,
			Internal: true,
		}
		go func() {
			if err := fw.Run(ctx, h); err != nil && ctx.Err() == nil {
				log.Warn("Config watch unavailable", "file", dir, logging.Err(err), "detail", "saving a linked file does not reload the config")
			}
		}()
	}
}
