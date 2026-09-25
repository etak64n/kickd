package main

// The operation subcommands: fire events and inspect the queue. They work
// on the same SQLite database as the agent ("kickd run"), so events fired
// while the agent is stopped run when it starts.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/etak64n/kickd/internal/config"
	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/queue"
	"github.com/etak64n/kickd/internal/runner"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailed  = 1   // an error, or a waited-for job that did not succeed
	exitUsage   = 2   // bad arguments
	exitTimeout = 124 // --timeout expired, as timeout(1) reports
)

// runOps runs one of the operation subcommands: event, events, queue,
// runs, show, cancel and status. It returns the exit code.
func runOps(args []string, stdout, stderr io.Writer) int {
	c := &cli{stdout: stdout, stderr: stderr}
	var err error
	switch args[0] {
	case "event":
		return c.event(args[1:])
	case "events":
		err = c.events(args[1:])
	case "queue":
		err = c.queue(args[1:])
	case "runs":
		err = c.runs(args[1:])
	case "show":
		err = c.show(args[1:])
	case "cancel":
		err = c.cancel(args[1:])
	case "status":
		err = c.status(args[1:])
	default:
		fmt.Fprintf(stderr, "kickd: unknown command %q\n", args[0])
		return exitUsage
	}
	return c.exit(err)
}

// opsCommands are the subcommands that runOps handles.
var opsCommands = map[string]bool{"event": true, "events": true, "queue": true, "runs": true, "show": true, "cancel": true, "status": true}

type cli struct {
	stdout, stderr io.Writer
}

// usageError marks errors caused by the arguments.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func (c *cli) exit(err error) int {
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(c.stderr, "kickd:", err)
	var ue usageError
	if errors.As(err, &ue) || errors.Is(err, flag.ErrHelp) {
		return exitUsage
	}
	return exitFailed
}

// parse parses flags that may appear before, between or after the
// positional arguments, and returns the positional ones.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			return nil, usageError{fmt.Sprintf("unknown flag %s", a)}
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			return nil, usageError{fmt.Sprintf("flag %s needs a value", a)}
		}
		i++
		flags = append(flags, args[i])
	}
	if err := fs.Parse(flags); err != nil {
		return nil, usageError{err.Error()}
	}
	return pos, nil
}

func configFlag(fs *flag.FlagSet) *string {
	p := fs.String("config", "", "config file")
	fs.StringVar(p, "c", "", "config file (shorthand)")
	return p
}

func (c *cli) load(flagValue string) (*config.Config, error) {
	return config.Load(config.Resolve(flagValue))
}

func (c *cli) open(cfg *config.Config) (*queue.Store, error) {
	st, err := queue.Open(cfg.Database.Path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("%w (kickd may run as another user; try sudo, or an administrator shell on Windows)", err)
		}
		return nil, err
	}
	return st, nil
}

func source() string {
	name := "unknown"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	return name + "@" + host
}

// event fires a named event: it adds a queued run to the database, which
// kickd consumes.
func (c *cli) event(args []string) int {
	fs := flag.NewFlagSet("event", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	dataJSON := fs.String("data", "", "parameters as a JSON object of strings")
	wait := fs.Bool("wait", false, "wait for the run and exit 0 only if it succeeds")
	timeout := fs.Duration("timeout", 0, "with --wait: give up after this long (exit 124)")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return c.exit(err)
	}
	if len(pos) == 0 {
		return c.exit(usageError{"event name is required: kickd event NAME [KEY=VALUE ...]"})
	}
	name, kvs := pos[0], pos[1:]
	cfg, err := c.load(*cfgPath)
	if err != nil {
		return c.exit(err)
	}
	def, ok := cfg.EventByName(name)
	if !ok {
		return c.exit(usageError{fmt.Sprintf("event %q is not defined in %s (defined: %s)", name, cfg.Path, strings.Join(cfg.EventNames(), ", "))})
	}
	if !def.Manual() {
		return c.exit(usageError{fmt.Sprintf("event %q has no manual trigger, so kickd event cannot fire it; add \"- type: manual\" to its triggers in %s", name, cfg.Path)})
	}
	data := map[string]string{}
	if *dataJSON != "" {
		if err := json.Unmarshal([]byte(*dataJSON), &data); err != nil {
			return c.exit(usageError{fmt.Sprintf("--data must be a JSON object of strings: %v", err)})
		}
	}
	for _, kv := range kvs {
		k, v, found := strings.Cut(kv, "=")
		if !found || k == "" {
			return c.exit(usageError{fmt.Sprintf("argument %q must be KEY=VALUE", kv)})
		}
		data[k] = v
	}
	data, err = def.Apply(data)
	if err != nil {
		return c.exit(usageError{err.Error()})
	}

	st, err := c.open(cfg)
	if err != nil {
		return c.exit(err)
	}
	defer st.Close()
	ctx := context.Background()
	src := source()
	ev := event.Event{
		RequestID: event.NewID(),
		Attempt:   1,
		Name:      name,
		Trigger:   event.KindManual,
		TriggerID: "manual",
		Time:      time.Now(),
		Data:      data,
		Source:    src,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return c.exit(err)
	}
	max := 0
	if def.Concurrency == config.ConcurrencyQueue {
		max = runner.MaxQueuedPerEvent
	}
	id, dropped, err := st.Enqueue(ctx, queue.Run{
		RequestID: ev.RequestID, Event: name, Trigger: event.KindManual, TriggerID: "manual", Source: src,
		Payload: payload, Attempt: 1, CreatedAt: ev.Time,
	}, max)
	if err != nil {
		return c.exit(err)
	}
	if dropped {
		fmt.Fprintf(c.stderr, "kickd: event %s already has %d queued runs; run %d was recorded as dropped\n", name, max, id)
		return exitFailed
	}
	if !c.agentAlive(ctx, st) {
		fmt.Fprintln(c.stderr, "kickd: the agent is not running; the run stays queued until \"kickd run\" or the service starts")
	}
	if !*wait {
		if *asJSON {
			return c.exit(printJSON(c.stdout, map[string]any{"runId": id, "requestId": ev.RequestID, "event": name, "status": queue.StatusQueued, "data": data}))
		}
		fmt.Fprintf(c.stdout, "queued run %d (event %s, request %s)\n", id, name, ev.RequestID)
		return exitOK
	}
	return c.waitRun(ctx, st, id, *timeout, *asJSON)
}

func (c *cli) agentAlive(ctx context.Context, st *queue.Store) bool {
	a, ok, err := st.AgentInfo(ctx)
	return err == nil && ok && a.Alive(runner.HeartbeatWindow)
}

// waitRun waits until the run reaches a final status. When kickd reruns
// it after an interruption, waitRun follows the new run.
func (c *cli) waitRun(ctx context.Context, st *queue.Store, id int64, timeout time.Duration, asJSON bool) int {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var chain []queue.Run
	cur := id
	for {
		r, err := st.GetRun(ctx, cur)
		switch {
		case err != nil && ctx.Err() == nil:
			return c.exit(err)
		case err == nil && r.Status == queue.StatusRetried:
			if next, ok, err := st.RetryOf(ctx, cur); err == nil && ok {
				chain = append(chain, r)
				cur = next.ID
				continue
			}
		case err == nil && queue.Final(r.Status):
			return c.reportRuns(append(chain, r), asJSON)
		}
		select {
		case <-ctx.Done():
			fmt.Fprintf(c.stderr, "kickd: timed out after %s waiting for run %d\n", timeout, cur)
			return exitTimeout
		case <-tick.C:
		}
	}
}

// reportRuns prints a run and the interrupted attempts before it. The exit
// code is 0 only when the last attempt succeeded.
func (c *cli) reportRuns(runs []queue.Run, asJSON bool) int {
	last := runs[len(runs)-1]
	code := exitOK
	if last.Status != queue.StatusSucceeded {
		code = exitFailed
	}
	if asJSON {
		if err := printJSON(c.stdout, runViews(runs)); err != nil {
			return c.exit(err)
		}
		return code
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "RUN\tEVENT\tATTEMPT\tSTATUS\tEXIT\tDURATION\tDETAIL\n")
	for _, r := range runs {
		fmt.Fprintf(tw, "%d\t%s\t%d\t%s\t%s\t%s\t%s\n", r.ID, r.Event, r.Attempt, statusText(r), exitText(r), durationText(r), orDash(reasonText(r)))
	}
	tw.Flush()
	return code
}

func (c *cli) events(args []string) error {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := c.load(*cfgPath)
	if err != nil {
		return err
	}
	if *asJSON {
		type view struct {
			Name        string         `json:"name"`
			Description string         `json:"description,omitempty"`
			Concurrency string         `json:"concurrency"`
			OnInterrupt string         `json:"onInterrupt"`
			MaxAttempts int            `json:"maxAttempts"`
			Triggers    []string       `json:"triggers"`
			Params      []config.Param `json:"params,omitempty"`
		}
		out := []view{}
		for _, e := range cfg.Events {
			out = append(out, view{e.Name, e.Description, e.Concurrency, e.OnInterrupt, e.MaxAttempts, triggerNames(e), e.Params})
		}
		return printJSON(c.stdout, out)
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "EVENT\tTRIGGERS\tCONCURRENCY\tON INTERRUPT\tPARAMS\tDESCRIPTION\n")
	for _, e := range cfg.Events {
		var params []string
		for _, p := range e.Params {
			switch {
			case p.Required:
				params = append(params, p.Name+" (required)")
			case p.Default != "":
				params = append(params, p.Name+"="+p.Default)
			default:
				params = append(params, p.Name)
			}
		}
		interrupt := e.OnInterrupt
		if e.OnInterrupt == config.InterruptRerun {
			interrupt = fmt.Sprintf("rerun (max %d)", e.MaxAttempts)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Name, strings.Join(triggerNames(e), ", "), e.Concurrency, interrupt,
			orDash(strings.Join(params, ", ")), orDash(e.Description))
	}
	return tw.Flush()
}

// triggerNames lists what fires an event; a manual firing always can.
func triggerNames(e config.Event) []string {
	out := []string{}
	for _, t := range e.Triggers {
		switch t.Type {
		case config.TriggerManual:
			out = append(out, "manual")
		case config.TriggerCron:
			out = append(out, "cron "+t.Schedule)
		case config.TriggerWebhook:
			out = append(out, "webhook "+t.Path)
		case config.TriggerFile:
			out = append(out, "file "+t.Path)
		}
	}
	return out
}

func (c *cli) queue(args []string) error {
	fs := flag.NewFlagSet("queue", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	return c.listRuns(*cfgPath, queue.Filter{Statuses: []string{queue.StatusQueued, queue.StatusRunning, queue.StatusInterrupted}}, *asJSON, true)
}

func (c *cli) runs(args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	name := fs.String("event", "", "only this event")
	status := fs.String("status", "", "only this status: queued, running, succeeded, failed, canceled, skipped, dropped, interrupted, retried, abandoned")
	limit := fs.Int("limit", 20, "number of runs")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	f := queue.Filter{Event: *name, Limit: *limit}
	if *status != "" {
		f.Statuses = []string{*status}
	}
	return c.listRuns(*cfgPath, f, *asJSON, false)
}

func (c *cli) listRuns(cfgPath string, f queue.Filter, asJSON, openOnly bool) error {
	cfg, err := c.load(cfgPath)
	if err != nil {
		return err
	}
	st, err := c.open(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	runs, err := st.ListRuns(context.Background(), f)
	if err != nil {
		return err
	}
	if openOnly {
		// Oldest first reads as the order in which the queue runs.
		sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	}
	if asJSON {
		return printJSON(c.stdout, runViews(runs))
	}
	if len(runs) == 0 {
		if openOnly {
			fmt.Fprintln(c.stdout, "the queue is empty")
		} else {
			fmt.Fprintln(c.stdout, "no runs")
		}
		return nil
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 2, 2, ' ', 0)
	if openOnly {
		fmt.Fprintf(tw, "RUN\tEVENT\tTRIGGER\tATTEMPT\tSTATUS\tQUEUED\tREQUEST\n")
		for _, r := range runs {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\t%s\t%s\n", r.ID, r.Event, r.TriggerID, r.Attempt, statusText(r), ago(r.CreatedAt), r.RequestID)
		}
	} else {
		fmt.Fprintf(tw, "RUN\tEVENT\tTRIGGER\tSTATUS\tEXIT\tDURATION\tFINISHED\tDETAIL\n")
		for _, r := range runs {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Event, r.TriggerID, statusText(r), exitText(r), durationText(r),
				timeText(r.FinishedAt), orDash(reasonText(r)))
		}
	}
	return tw.Flush()
}

func (c *cli) show(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	id, err := runID(pos)
	if err != nil {
		return err
	}
	cfg, err := c.load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := c.open(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	r, err := st.GetRun(context.Background(), id)
	if errors.Is(err, queue.ErrNotFound) {
		return fmt.Errorf("run %d not found (finished runs are kept for %s)", id, cfg.Database.Retention)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		v := runViews([]queue.Run{r})[0]
		v.Output = r.Output
		var payload any
		if json.Unmarshal(r.Payload, &payload) == nil {
			v.Payload = payload
		}
		return printJSON(c.stdout, v)
	}
	tw := tabwriter.NewWriter(c.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "run\t%d\n", r.ID)
	fmt.Fprintf(tw, "event\t%s\n", r.Event)
	fmt.Fprintf(tw, "trigger\t%s\n", r.TriggerID)
	if r.Source != "" {
		fmt.Fprintf(tw, "source\t%s\n", r.Source)
	}
	fmt.Fprintf(tw, "request\t%s\n", r.RequestID)
	fmt.Fprintf(tw, "attempt\t%d\n", r.Attempt)
	if r.RetryOf != 0 {
		fmt.Fprintf(tw, "rerun of\trun %d\n", r.RetryOf)
	}
	fmt.Fprintf(tw, "status\t%s\n", statusText(r))
	if r.Reason != "" {
		fmt.Fprintf(tw, "reason\t%s\n", r.Reason)
	}
	if r.Detail != "" {
		fmt.Fprintf(tw, "detail\t%s\n", r.Detail)
	}
	fmt.Fprintf(tw, "exit\t%s\n", exitText(r))
	if r.Signal != "" {
		fmt.Fprintf(tw, "signal\t%s\n", r.Signal)
	}
	fmt.Fprintf(tw, "queued\t%s\n", timeText(r.CreatedAt))
	fmt.Fprintf(tw, "started\t%s\n", timeText(r.StartedAt))
	fmt.Fprintf(tw, "finished\t%s\n", timeText(r.FinishedAt))
	fmt.Fprintf(tw, "duration\t%s\n", durationText(r))
	if r.Skipped > 0 {
		fmt.Fprintf(tw, "skipped\t%d firings arrived while this run was active\n", r.Skipped)
	}
	tw.Flush()
	var payload event.Event
	if json.Unmarshal(r.Payload, &payload) == nil && len(payload.Data) > 0 {
		keys := make([]string, 0, len(payload.Data))
		for k := range payload.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintln(c.stdout, "--- parameters ---")
		for _, k := range keys {
			fmt.Fprintf(c.stdout, "%s=%s\n", k, payload.Data[k])
		}
	}
	if r.Output != "" {
		fmt.Fprintln(c.stdout, "--- output ---")
		fmt.Fprint(c.stdout, r.Output)
		if !strings.HasSuffix(r.Output, "\n") {
			fmt.Fprintln(c.stdout)
		}
		if r.OutputTruncated {
			fmt.Fprintln(c.stdout, "--- output truncated at 64 KB ---")
		}
	}
	return nil
}

func (c *cli) cancel(args []string) error {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	id, err := runID(pos)
	if err != nil {
		return err
	}
	cfg, err := c.load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := c.open(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	r, err := st.RequestCancel(ctx, id)
	if errors.Is(err, queue.ErrNotFound) {
		return fmt.Errorf("run %d not found", id)
	}
	if err != nil {
		return err
	}
	switch {
	case r.Status == queue.StatusCanceled && !r.CancelRequested:
		fmt.Fprintf(c.stdout, "canceled run %d (%s); it had not started\n", r.ID, r.Event)
	case r.CancelRequested:
		fmt.Fprintf(c.stdout, "asked kickd to stop run %d (%s)\n", r.ID, r.Event)
		if !c.agentAlive(ctx, st) {
			fmt.Fprintln(c.stderr, "kickd: the agent is not running; it handles the run as interrupted when it starts")
		}
	default:
		return fmt.Errorf("run %d is %s and cannot be canceled", r.ID, r.Status)
	}
	return nil
}

func (c *cli) status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := c.load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := c.open(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	a, known, err := st.AgentInfo(ctx)
	if err != nil {
		return err
	}
	queued, running, err := st.Counts(ctx)
	if err != nil {
		return err
	}
	interrupted, err := st.ListRuns(ctx, queue.Filter{Statuses: []string{queue.StatusInterrupted}})
	if err != nil {
		return err
	}
	alive := known && a.Alive(runner.HeartbeatWindow)
	if *asJSON {
		out := map[string]any{"running": alive, "queued": queued, "runningRuns": running, "interrupted": len(interrupted), "database": st.Path()}
		if known {
			out["agent"] = map[string]any{"pid": a.PID, "version": a.Version, "host": a.Host,
				"startedAt": a.StartedAt.UTC().Format(time.RFC3339), "heartbeatAt": a.HeartbeatAt.UTC().Format(time.RFC3339)}
		}
		return printJSON(c.stdout, out)
	}
	switch {
	case alive:
		fmt.Fprintf(c.stdout, "agent: running (pid %d, version %s, since %s, heartbeat %s)\n", a.PID, a.Version, timeText(a.StartedAt), ago(a.HeartbeatAt))
	case known && !a.StoppedAt.IsZero():
		fmt.Fprintf(c.stdout, "agent: stopped at %s\n", timeText(a.StoppedAt))
	case known:
		fmt.Fprintf(c.stdout, "agent: not responding (last heartbeat %s, pid %d)\n", ago(a.HeartbeatAt), a.PID)
	default:
		fmt.Fprintln(c.stdout, "agent: has not started with this database yet")
	}
	fmt.Fprintf(c.stdout, "queue: %d queued, %d running, %d interrupted\n", queued, running, len(interrupted))
	fmt.Fprintf(c.stdout, "database: %s\n", st.Path())
	return nil
}

func runID(pos []string) (int64, error) {
	if len(pos) != 1 {
		return 0, usageError{"one RUN_ID is required"}
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(pos[0], "#"), 10, 64)
	if err != nil || id <= 0 {
		return 0, usageError{fmt.Sprintf("RUN_ID %q must be a positive number", pos[0])}
	}
	return id, nil
}

type runView struct {
	ID         int64  `json:"runId"`
	RequestID  string `json:"requestId"`
	Event      string `json:"event"`
	Trigger    string `json:"trigger"`
	TriggerID  string `json:"triggerId"`
	Source     string `json:"source,omitempty"`
	Attempt    int    `json:"attempt"`
	RetryOf    int64  `json:"retryOf,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Detail     string `json:"detail,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Skipped    int    `json:"skipped,omitempty"`
	CreatedAt  string `json:"queuedAt"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	Output     string `json:"output,omitempty"`
	Payload    any    `json:"payload,omitempty"`
}

func runViews(runs []queue.Run) []runView {
	out := make([]runView, 0, len(runs))
	for _, r := range runs {
		v := runView{ID: r.ID, RequestID: r.RequestID, Event: r.Event, Trigger: r.Trigger, TriggerID: r.TriggerID, Source: r.Source,
			Attempt: r.Attempt, RetryOf: r.RetryOf, Status: r.Status, Reason: r.Reason, Detail: r.Detail, ExitCode: r.ExitCode,
			Signal: r.Signal, Skipped: r.Skipped, CreatedAt: rfc(r.CreatedAt), StartedAt: rfc(r.StartedAt), FinishedAt: rfc(r.FinishedAt)}
		if !r.FinishedAt.IsZero() && !r.StartedAt.IsZero() {
			d := r.Duration.Milliseconds()
			v.DurationMs = &d
		}
		out = append(out, v)
	}
	return out
}

func reasonText(r queue.Run) string {
	switch {
	case r.Reason != "" && r.Detail != "":
		return r.Reason + ": " + r.Detail
	case r.Reason != "":
		return r.Reason
	}
	return r.Detail
}

func rfc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func statusText(r queue.Run) string {
	if r.Status == queue.StatusRunning && r.CancelRequested {
		return "running (cancel requested)"
	}
	return r.Status
}

func exitText(r queue.Run) string {
	if r.ExitCode == nil {
		return "-"
	}
	return strconv.Itoa(*r.ExitCode)
}

func durationText(r queue.Run) string {
	switch {
	case r.ExitCode == nil && r.Status != queue.StatusRunning:
		// Never ran, or cut off without an exit (a crash).
		return "-"
	case !r.FinishedAt.IsZero() && !r.StartedAt.IsZero():
		return r.Duration.Round(time.Millisecond).String()
	case r.Status == queue.StatusRunning && !r.StartedAt.IsZero():
		return time.Since(r.StartedAt).Round(time.Second).String() + " so far"
	}
	return "-"
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return timeText(t)
}
