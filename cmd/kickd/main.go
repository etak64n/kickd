// Command kickd is an always-on agent that runs configured commands when
// files change, on cron schedules, or when a webhook is called.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	// Windows has no IANA time zone database, and a minimal Linux may lack
	// one; the embedded copy keeps the timezone of cron triggers working.
	_ "time/tzdata"

	"github.com/kardianos/service"

	"github.com/etak64n/kickd/internal/agent"
	"github.com/etak64n/kickd/internal/config"
	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/licenses"
	"github.com/etak64n/kickd/internal/logging"
)

const usageText = `kickd - run named events from cron, webhooks, file changes or the command line

Usage:
  kickd run     [-c CONFIG] [--name NAME]   Run the agent in the foreground, or as a service when started by one
  kickd event   NAME [KEY=VALUE ...] [--data JSON] [--wait] [--timeout DURATION] [--json]
                                           Fire an event; --wait waits for the run and exits 0 only if it succeeds
  kickd events  [--json]                   List the events and what fires them
  kickd queue   [--json]                   Show queued, running and interrupted runs
  kickd runs    [--event NAME] [--status STATUS] [--limit N] [--json]
                                           Show recent runs, newest first
  kickd show    RUN_ID [--json]            Show one run, including its output
  kickd cancel  RUN_ID                     Cancel a queued run, or stop a running one
  kickd status  [--json]                   Show whether the agent is running and the queue size
  kickd check   [-c CONFIG]                Validate the config and print a summary
  kickd init    [-c CONFIG]                Write an example config
  kickd service ACTION [-c CONFIG] [--user] [--name NAME]
                ACTION: install | uninstall | start | stop | restart | status
  kickd licenses                           Print the licenses of kickd and of the software it includes
  kickd version

Every command takes -c CONFIG. The config is found in this order:
-c, $KICKD_CONFIG, <user config dir>/kickd/config.yaml, ./kickd.yaml
`

func main() {
	info, ok := debug.ReadBuildInfo()
	version = resolveVersion(version, info, ok)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "init":
		err = cmdInit(os.Args[2:])
	case "service":
		err = cmdService(os.Args[2:])
	case "event", "events", "queue", "runs", "show", "cancel", "status":
		os.Exit(runOps(os.Args[1:], os.Stdout, os.Stderr))
	case "licenses":
		fmt.Print(licenses.Text)
	case "version", "-v", "--version":
		fmt.Println("kickd " + version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "kickd: unknown command %q\n\n%s", os.Args[1], usageText)
		os.Exit(2)
	}
	if err != nil {
		switch {
		case errors.Is(err, flag.ErrHelp):
			os.Exit(2)
		case errors.Is(err, errLogged):
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "kickd:", err)
		os.Exit(1)
	}
}

// errLogged tells main that the failure is already in the log as a FATAL
// line, so it exits without printing it again.
var errLogged = errors.New("failure already logged")

func newFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("kickd "+name, flag.ContinueOnError)
	cfg := fs.String("config", "", "config file path")
	fs.StringVar(cfg, "c", "", "config file path (shorthand)")
	return fs, cfg
}

func cmdRun(args []string) error {
	fs, cfgFlag := newFlags("run")
	name := fs.String("name", "kickd", "service name (must match the installed service)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	procID := event.NewID()
	interactive := service.Interactive()
	cfgPath := config.Resolve(*cfgFlag)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		// The log settings live in the config, so this line uses the
		// defaults and the LOG_LEVEL and LOG_FORMAT overrides.
		fatal(procID, logging.Options{Level: os.Getenv(config.EnvLogLevel), Format: os.Getenv(config.EnvLogFormat)},
			"Config load failed", err, "file", cfgPath)
		return errLogged
	}
	logger, closer, err := logging.New(logging.Options{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		File:       cfg.Log.Path,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		Console:    interactive,
	})
	if err != nil {
		fatal(procID, logging.Options{Level: cfg.Log.Level, Format: cfg.Log.Format}, "Log file open failed", err, "file", cfg.Log.Path)
		return errLogged
	}
	defer closer.Close()
	plog := logger.With("requestId", procID)
	if limit, ok := openFileLimit(); ok {
		plog.Debug("Open file limit", "count", limit)
	}
	opts := agent.Options{ConfigPath: cfg.Path, Version: version, ProcessID: procID}

	if interactive {
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigs)
		go func() {
			select {
			case s := <-sigs:
				cancel(agent.SignalCause{Signal: s})
			case <-ctx.Done():
			}
		}()
		if err := agent.Run(ctx, logger, opts); err != nil {
			plog.Log(context.Background(), logging.LevelFatal, "Agent start failed", logging.Err(err), "exitCode", 1)
			return errLogged
		}
		return nil
	}
	svc, err := service.New(&program{logger: logger, opts: opts}, serviceConfig(cfg.Path, *name, false))
	if err != nil {
		plog.Log(context.Background(), logging.LevelFatal, "Service setup failed", logging.Err(err), "exitCode", 1)
		return errLogged
	}
	if err := svc.Run(); err != nil {
		plog.Log(context.Background(), logging.LevelFatal, "Service run failed", logging.Err(err), "exitCode", 1)
		return errLogged
	}
	return nil
}

// fatal writes one FATAL line to stderr with a logger built from o.
func fatal(procID string, o logging.Options, msg string, err error, kv ...any) {
	logger, closer, lerr := logging.New(o)
	if lerr != nil {
		fmt.Fprintln(os.Stderr, "kickd:", err)
		return
	}
	defer closer.Close()
	args := append([]any{"requestId", procID}, kv...)
	args = append(args, logging.Err(err), "exitCode", 1)
	logger.Log(context.Background(), logging.LevelFatal, msg, args...)
}

func cmdCheck(args []string) error {
	fs, cfgFlag := newFlags("check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(config.Resolve(*cfgFlag))
	if err != nil {
		return err
	}
	triggers := 0
	for _, e := range cfg.Events {
		triggers += len(e.Triggers)
	}
	fmt.Printf("OK: %s (%d events, %d triggers)\n", cfg.Path, len(cfg.Events), triggers)
	logPath := cfg.Log.Path
	if logPath == "" {
		logPath = "standard error"
	}
	fmt.Printf("log: %s (level=%s%s format=%s%s max_size_mb=%d max_backups=%d)\n",
		logPath, cfg.Log.Level, fromEnv(cfg.Log.LevelFrom), cfg.Log.Format, fromEnv(cfg.Log.FormatFrom),
		cfg.Log.MaxSizeMB, cfg.Log.MaxBackups)
	fmt.Printf("database: %s (retention %s)\n", cfg.Database.Path, cfg.Database.Retention)
	switch {
	case cfg.WebhookServer():
		fmt.Printf("webhook: listen=%s\n", cfg.Webhook.Listen)
	case cfg.HasWebhook():
		fmt.Println("webhook: disabled by webhook.enabled, so webhook triggers do not fire")
	}
	for _, e := range cfg.Events {
		// A list is shown as a list, so that it does not read as a string
		// for the shell.
		cmd := e.Shell
		if cmd == "" {
			quoted := make([]string, len(e.Command))
			for i, a := range e.Command {
				quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
			}
			cmd = "[" + strings.Join(quoted, ", ") + "]"
		}
		interrupt := e.OnInterrupt
		if e.OnInterrupt == config.InterruptRerun {
			interrupt = fmt.Sprintf("rerun, up to %d attempts", e.MaxAttempts)
		}
		fmt.Printf("- %s [%s; on interrupt: %s]: %s\n", e.Name, e.Concurrency, interrupt, cmd)
		fmt.Printf("    workdir  %s\n", e.Workdir)
		for k, v := range e.Env {
			if strings.EqualFold(k, "PATH") {
				fmt.Printf("    PATH     %s\n", v)
			}
		}
		fmt.Printf("    %s\n", describeKick(e))
		for _, t := range e.Triggers {
			fmt.Printf("    %s\n", describeTrigger(t))
		}
	}
	for _, w := range cfg.Warnings() {
		fmt.Printf("warning: event %q: %s\n", w.Event, w.Detail)
	}
	return nil
}

func describeKick(e config.Event) string {
	s := "manual   kickd event " + e.Name
	for _, p := range e.Params {
		switch {
		case p.Required:
			s += " " + p.Name + "=..."
		case p.Default != "":
			s += " [" + p.Name + "=" + p.Default + "]"
		default:
			s += " [" + p.Name + "=...]"
		}
	}
	return s
}

func describeTrigger(t config.Trigger) string {
	switch t.Type {
	case config.TriggerFile:
		s := fmt.Sprintf("file     %s (changes=%s, debounce=%s", t.Path, strings.Join(t.Changes, ","), t.Debounce)
		if t.Recursive {
			s += ", recursive"
		}
		if len(t.Include) > 0 {
			s += ", include=" + strings.Join(t.Include, ",")
		}
		if len(t.Exclude) > 0 {
			s += ", exclude=" + strings.Join(t.Exclude, ",")
		}
		return s + ")"
	case config.TriggerCron:
		var details []string
		if t.Timezone != "" {
			details = append(details, t.Timezone)
		}
		details = append(details, "missed="+t.Missed)
		return "cron     " + t.Schedule + " (" + strings.Join(details, ", ") + ")"
	case config.TriggerWebhook:
		methods := "ANY"
		if len(t.Methods) > 0 {
			methods = strings.Join(t.Methods, ",")
		}
		var auth []string
		if t.Token != "" {
			auth = append(auth, "token")
		}
		if t.Secret != "" {
			auth = append(auth, "secret")
		}
		if len(auth) == 0 {
			auth = append(auth, "no auth")
		}
		s := fmt.Sprintf("webhook  %s %s (%s", methods, t.Path, strings.Join(auth, "+"))
		if t.Wait {
			s += ", wait"
		}
		return s + ")"
	}
	return t.Type
}

// fromEnv marks a log setting that an environment variable overrides.
func fromEnv(source string) string {
	if strings.HasPrefix(source, "log.") {
		return ""
	}
	return " (from " + source + ")"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdInit(args []string) error {
	fs, cfgFlag := newFlags("init")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := *cfgFlag
	if path == "" {
		path = os.Getenv("KICKD_CONFIG")
	}
	if path == "" {
		path = config.DefaultPath()
	}
	if fileExists(path) {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	system := initForSystem(path)
	if err := os.WriteFile(path, []byte(config.Example(runtime.GOOS, system)), 0o600); err != nil {
		return err
	}
	p := config.PathsFor(runtime.GOOS, system)
	fmt.Printf("wrote %s\n", path)
	fmt.Printf("log:      %s\ndatabase: %s\n", p.Log, p.Database)
	fmt.Printf("Edit it, then run: kickd check -c %s\n", path)
	return nil
}

// initForSystem reports whether a config file at path is for a service of
// the whole system: a file outside the home directory, such as
// /etc/kickd/config.yaml or C:\ProgramData\kickd\config.yaml.
func initForSystem(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(home, abs)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cmdService(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("service action is required: install, uninstall, start, stop, restart, status")
	}
	action := args[0]
	fs, cfgFlag := newFlags("service " + action)
	user := fs.Bool("user", false, "per-user service (macOS LaunchAgent, systemd --user)")
	name := fs.String("name", "kickd", "service name")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	abs, err := filepath.Abs(config.Resolve(*cfgFlag))
	if err != nil {
		return err
	}
	if action == "install" {
		if _, err := config.Load(abs); err != nil {
			return fmt.Errorf("config must be valid before installing: %w", err)
		}
	}
	svc, err := service.New(&program{}, serviceConfig(abs, *name, *user))
	if err != nil {
		return err
	}
	switch action {
	case "status":
		st, err := svc.Status()
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", *name, statusName(st))
		return nil
	case "install", "uninstall", "start", "stop", "restart":
		if err := service.Control(svc, action); err != nil {
			return err
		}
		fmt.Printf("%s: %s done\n", *name, action)
		if action == "install" {
			// Later service commands must repeat --user and --name to find
			// the same service definition.
			flags := ""
			if *user {
				flags += " --user"
			}
			if *name != "kickd" {
				flags += " --name " + *name
			}
			fmt.Printf("config: %s\nstart it with: kickd service start%s\n", abs, flags)
		}
		return nil
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}

func statusName(st service.Status) string {
	switch st {
	case service.StatusRunning:
		return "running"
	case service.StatusStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func serviceConfig(cfgPath, name string, user bool) *service.Config {
	opts := service.KeyValue{
		"Restart":                "always",  // systemd
		"ReloadSignal":           "HUP",     // systemd: systemctl reload
		"OnFailure":              "restart", // windows recovery
		"OnFailureDelayDuration": "5s",
		"OnFailureResetPeriod":   10,
		"KeepAlive":              true, // launchd
		"RunAtLoad":              true,
	}
	if user {
		opts["UserService"] = true
	}
	return &service.Config{
		Name:             name,
		DisplayName:      name + " (event-driven command runner)",
		Description:      "Runs configured commands on file changes, cron schedules and webhooks.",
		Arguments:        []string{"run", "--config", cfgPath, "--name", name},
		WorkingDirectory: filepath.Dir(cfgPath),
		Option:           opts,
	}
}

// program adapts the agent to the service manager.
type program struct {
	logger *slog.Logger
	opts   agent.Options
	cancel context.CancelCauseFunc
	done   chan struct{}
}

func (p *program) Start(service.Service) error {
	ctx, cancel := context.WithCancelCause(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		if err := agent.Run(ctx, p.logger, p.opts); err != nil {
			p.logger.Log(context.Background(), logging.LevelFatal, "Agent start failed",
				"requestId", p.opts.ProcessID, logging.Err(err), "exitCode", 1)
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	p.cancel(agent.ErrServiceStop)
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		p.logger.Warn("Agent stop timed out", "requestId", p.opts.ProcessID, "thresholdMs", stopTimeout.Milliseconds())
	}
	return nil
}

// stopTimeout bounds how long the service manager's stop request waits
// for running commands.
const stopTimeout = 30 * time.Second

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
