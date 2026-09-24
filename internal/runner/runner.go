// Package runner executes job commands and applies concurrency policies.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
)

// Concurrency policies.
const (
	PolicySkip     = "skip"
	PolicyQueue    = "queue"
	PolicyParallel = "parallel"
)

// Stdin modes.
const (
	StdinNone    = "none"
	StdinPayload = "payload"
)

// Interruption handling, as config.Event.OnInterrupt spells it.
const (
	InterruptAbandon = "abandon"
	InterruptRerun   = "rerun"
)

const (
	maxCapturedOutput = 64 * 1024
	maxLogLine        = 8 * 1024
	tailLines         = 20
	tailBytes         = 4 * 1024
)

// killGrace is how long a stopped process gets to exit, and how long kickd
// waits for the output pipes to close, before it kills the process. Tests
// shorten it.
var killGrace = 10 * time.Second

// Spec is what the runner needs to know about a named event.
type Spec struct {
	Name        string
	Command     []string
	Shell       string
	Workdir     string
	Env         map[string]string
	Timeout     time.Duration
	Concurrency string
	OnInterrupt string
	MaxAttempts int
	Stdin       string
	LogOutput   bool
	// Defaults fills in parameters that a firing does not set.
	Defaults map[string]string
}

// Stats counts job runs since the runner was created.
type Stats struct {
	Processed   int64 // runs that finished, whatever the outcome
	Succeeded   int64
	Failed      int64
	Canceled    int64 // stopped with kickd cancel
	Interrupted int64 // cut off by kickd stopping
	Skipped     int64 // firings not run because the event was running
	Dropped     int64 // firings not run because the queue was full
}

// Runner executes jobs. Every run is bound to the runner context, so
// cancelling it terminates all running commands.
type Runner struct {
	ctx      context.Context
	base     *slog.Logger
	log      *slog.Logger
	inflight *inflight

	processed, succeeded, failed, canceled, interrupted, skipped, dropped atomic.Int64
}

// New creates a runner. base is a logger without requestId; procID is the
// request ID used for lines that do not belong to one run.
func New(ctx context.Context, base *slog.Logger, procID string) *Runner {
	return &Runner{ctx: ctx, base: base, log: base.With("requestId", procID), inflight: newInflight()}
}

// Wait blocks until every in-flight run has finished.
func (r *Runner) Wait() { r.inflight.wait() }

// InFlight returns the number of commands running now.
func (r *Runner) InFlight() int { return r.inflight.count() }

// Stats returns the run counters.
func (r *Runner) Stats() Stats {
	return Stats{
		Processed:   r.processed.Load(),
		Succeeded:   r.succeeded.Load(),
		Failed:      r.failed.Load(),
		Canceled:    r.canceled.Load(),
		Interrupted: r.interrupted.Load(),
		Skipped:     r.skipped.Load(),
		Dropped:     r.dropped.Load(),
	}
}

// inflight counts running commands. Unlike sync.WaitGroup it tolerates an
// increment while wait is blocked, which happens when a trigger fires
// during shutdown.
type inflight struct {
	mu   sync.Mutex
	cond sync.Cond
	n    int
}

func newInflight() *inflight {
	f := &inflight{}
	f.cond.L = &f.mu
	return f
}

func (f *inflight) add(d int) {
	f.mu.Lock()
	f.n += d
	if f.n <= 0 {
		f.cond.Broadcast()
	}
	f.mu.Unlock()
}

func (f *inflight) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func (f *inflight) wait() {
	f.mu.Lock()
	for f.n > 0 {
		f.cond.Wait()
	}
	f.mu.Unlock()
}

// Execute runs job once for ev and blocks until it finishes. ctx bounds
// the run in addition to the runner context and the job timeout.
func (r *Runner) Execute(ctx context.Context, spec Spec, ev event.Event) event.Result {
	r.inflight.add(1)
	defer r.inflight.add(-1)
	return r.execute(ctx, spec, ev, nil)
}

// spawn runs job in a new goroutine and counts it as in flight from now.
// ctx bounds the run in addition to the runner context; done runs after
// the command has finished; summary adds keys to the completion line.
func (r *Runner) spawn(ctx context.Context, spec Spec, ev event.Event, summary func() []any, done func(event.Result)) {
	r.inflight.add(1)
	go func() {
		defer r.inflight.add(-1)
		res := r.execute(ctx, spec, ev, summary)
		if done != nil {
			done(res)
		}
	}()
}

func (r *Runner) execute(ctx context.Context, spec Spec, ev event.Event, summary func() []any) (res event.Result) {
	if ev.RequestID == "" {
		ev.RequestID = event.NewID()
	}
	log := r.base.With("requestId", ev.RequestID, "event", spec.Name, "trigger", ev.Trigger)
	if ev.RunID != 0 {
		log = log.With("runId", ev.RunID)
	}
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			res = event.Result{ExitCode: -1, Duration: time.Since(start), Error: "panic", Reason: "panic"}
			r.processed.Add(1)
			r.failed.Add(1)
			log.Error("Run failed", "reason", "panic", "durationMs", res.Duration.Milliseconds(), logging.Panic(p, debug.Stack()))
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	if spec.Timeout > 0 {
		var tcancel context.CancelFunc
		ctx, tcancel = context.WithTimeout(ctx, spec.Timeout)
		defer tcancel()
	}

	// Keep the event file consistent with the log, which is in UTC.
	ev.Time = ev.Time.UTC()
	payload, err := json.Marshal(ev)
	if err != nil {
		return r.startFailure(log, start, "event_encode", err)
	}
	eventFile, err := writeEventFile(payload)
	if err != nil {
		return r.startFailure(log, start, "event_file", err)
	}
	defer func() {
		if err := os.Remove(eventFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn("Payload file cleanup failed", "file", eventFile, logging.Err(err))
		}
	}()

	env := buildEnv(spec, ev, eventFile)
	cmd := buildCommand(ctx, spec, env)
	cmd.Dir = spec.Workdir
	cmd.Env = env
	if spec.Stdin == StdinPayload {
		cmd.Stdin = bytes.NewReader(payload)
	}
	capture := &capBuffer{limit: maxCapturedOutput}
	tail := &tailBuffer{maxLines: tailLines, maxBytes: tailBytes}
	stdout := &lineWriter{log: log, stream: "stdout", capture: capture, emit: spec.LogOutput}
	stderr := &lineWriter{log: log, stream: "stderr", capture: capture, tail: tail, emit: spec.LogOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setSysProcAttr(cmd)
	cmd.Cancel = func() error { return terminate(cmd) }
	cmd.WaitDelay = killGrace

	log.Info("Run started", append([]any{"triggerId", ev.TriggerID}, eventAttrs(ev)...)...)
	if log.Enabled(context.Background(), slog.LevelDebug) {
		log.Debug("Running command", "command", logging.Mask(displayCommand(spec)), "workdir", spec.Workdir, "file", eventFile)
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return r.canceledBeforeStart(log, ctx, start)
		}
		return r.startFailure(log, start, "exec", err)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		if err := killTree(cmd); err != nil {
			log.Warn("Process group kill failed", logging.Err(err))
		}
	}
	stdout.flush()
	stderr.flush()

	res = event.Result{
		ExitCode:  cmd.ProcessState.ExitCode(),
		Duration:  time.Since(start),
		Output:    capture.String(),
		Truncated: capture.truncated,
	}
	msg, level, reason := "Run completed", slog.LevelInfo, ""
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.Error, reason = "timeout", "timeout"
		msg, level = "Run failed", slog.LevelError
		r.failed.Add(1)
	case ctx.Err() != nil:
		res.Error, reason = "canceled", "shutdown"
		msg, level = "Run interrupted", slog.LevelWarn
		if errors.Is(context.Cause(ctx), event.ErrCanceledByUser) {
			reason, msg = "canceled_by_user", "Run canceled"
			r.canceled.Add(1)
		} else {
			r.interrupted.Add(1)
		}
	case waitErr != nil && res.ExitCode == 0:
		res.Error, reason = "wait_failed", "wait_failed"
		msg, level = "Run failed", slog.LevelError
		r.failed.Add(1)
	case res.ExitCode != 0:
		reason = "exit_code"
		msg, level = "Run failed", slog.LevelError
		r.failed.Add(1)
	default:
		r.succeeded.Add(1)
	}
	r.processed.Add(1)
	res.Reason = reason
	res.Signal = exitSignal(cmd.ProcessState)

	var attrs []any
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	attrs = append(attrs, "exitCode", res.ExitCode)
	if res.Signal != "" {
		attrs = append(attrs, "signal", res.Signal)
	}
	attrs = append(attrs, "durationMs", res.Duration.Milliseconds())
	if reason == "timeout" {
		attrs = append(attrs, "thresholdMs", spec.Timeout.Milliseconds())
	}
	if summary != nil {
		attrs = append(attrs, summary()...)
	}
	if spec.LogOutput && res.ExitCode != 0 {
		if t := tail.String(); t != "" {
			attrs = append(attrs, "stderrTail", logging.Mask(t))
		}
	}
	if reason == "wait_failed" {
		attrs = append(attrs, logging.Err(waitErr))
	}
	log.Log(context.Background(), level, msg, attrs...)
	return res
}

// canceledBeforeStart ends a run whose context was canceled before its
// process started: a stop of kickd interrupts the run, and a cancel by the
// user cancels it, as they would while the process runs.
func (r *Runner) canceledBeforeStart(log *slog.Logger, ctx context.Context, start time.Time) event.Result {
	res := event.Result{ExitCode: -1, Duration: time.Since(start), Error: "canceled", Reason: "shutdown"}
	msg := "Run interrupted"
	if errors.Is(context.Cause(ctx), event.ErrCanceledByUser) {
		res.Reason, msg = "canceled_by_user", "Run canceled"
		r.canceled.Add(1)
	} else {
		r.interrupted.Add(1)
	}
	r.processed.Add(1)
	log.Warn(msg, "reason", res.Reason, "exitCode", res.ExitCode, "durationMs", res.Duration.Milliseconds())
	return res
}

func (r *Runner) startFailure(log *slog.Logger, start time.Time, stage string, err error) event.Result {
	d := time.Since(start)
	r.processed.Add(1)
	r.failed.Add(1)
	log.Error("Run failed", "reason", "start_failed", "detail", stage, "durationMs", d.Milliseconds(), logging.Err(err))
	return event.Result{ExitCode: -1, Duration: d, Error: "start_failed", Reason: "start_failed", Output: err.Error()}
}

// eventAttrs describes the firing on the "Run started" line.
func eventAttrs(ev event.Event) []any {
	var out []any
	if ev.Attempt > 1 {
		out = append(out, "attempt", ev.Attempt)
	}
	switch {
	case len(ev.Files) > 0:
		out = append(out, "count", len(ev.Files), "file", ev.Files[len(ev.Files)-1].Path)
	case ev.Webhook != nil:
		out = append(out, "method", ev.Webhook.Method)
	case ev.Source != "":
		out = append(out, "source", ev.Source)
	}
	return out
}

func buildCommand(ctx context.Context, spec Spec, env []string) *exec.Cmd {
	if spec.Shell != "" {
		return shellCommand(ctx, spec.Shell)
	}
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)
	// A bare program name is looked up in the PATH that the command gets,
	// as the shell of a shell command does, instead of in the PATH of
	// kickd. A name with a path starts at the working directory.
	if name := spec.Command[0]; !strings.ContainsAny(name, `/\`) {
		cmd.Path, cmd.Err = lookPath(name, env)
	}
	return cmd
}

// buildEnv layers the job environment and the event variables over the
// agent environment. Values in spec.Env may reference ${VAR}.
func buildEnv(spec Spec, ev event.Event, eventFile string) []string {
	env := os.Environ()
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+os.ExpandEnv(spec.Env[k]))
	}
	env = append(env, ev.Env()...)
	return append(env, "KICKD_PAYLOAD_FILE="+eventFile)
}

func writeEventFile(payload []byte) (string, error) {
	f, err := os.CreateTemp("", "kickd-payload-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(payload); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func displayCommand(spec Spec) string {
	s := spec.Shell
	if s == "" {
		s = strings.Join(spec.Command, " ")
	}
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	return s
}

// capBuffer keeps the first limit bytes of the combined output.
type capBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *capBuffer) Write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return
	}
	if len(p) > room {
		p = p[:room]
		b.truncated = true
	}
	b.buf.Write(p)
}

func (b *capBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tailBuffer keeps the last lines of a stream for the failure line.
type tailBuffer struct {
	mu       sync.Mutex
	lines    []string
	partial  []byte
	maxLines int
	maxBytes int
}

func (t *tailBuffer) Write(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial = append(t.partial, p...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.push(string(bytes.TrimRight(t.partial[:i], "\r")))
		t.partial = t.partial[i+1:]
	}
	if len(t.partial) > t.maxBytes {
		t.partial = t.partial[len(t.partial)-t.maxBytes:]
	}
}

func (t *tailBuffer) push(line string) {
	t.lines = append(t.lines, line)
	if len(t.lines) > t.maxLines {
		t.lines = t.lines[len(t.lines)-t.maxLines:]
	}
}

// String returns the kept lines, trimmed to maxBytes from the end.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	if len(t.partial) > 0 {
		lines = append(slicesClone(lines), string(t.partial))
	}
	s := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if len(s) > t.maxBytes {
		s = s[len(s)-t.maxBytes:]
	}
	return s
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// lineWriter captures a stream and logs it line by line at DEBUG.
type lineWriter struct {
	log     *slog.Logger
	stream  string
	capture *capBuffer
	tail    *tailBuffer
	emit    bool
	buf     []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.capture.Write(p)
	if w.tail != nil {
		w.tail.Write(p)
	}
	if !w.emit || !w.log.Enabled(context.Background(), slog.LevelDebug) {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxLogLine {
		w.line(w.buf)
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.line(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) line(b []byte) {
	b = bytes.TrimRight(b, "\r")
	if len(b) > maxLogLine {
		b = b[:maxLogLine]
	}
	w.log.Debug("Run output", "stream", w.stream, "text", logging.Mask(string(b)))
}
