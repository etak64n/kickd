package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
	"github.com/etak64n/kickd/internal/queue"
)

const (
	// MaxQueuedPerEvent bounds the backlog of one event under the queue
	// policy; further firings are recorded as dropped.
	MaxQueuedPerEvent = 1000
	// pollEvery is how often the loop checks the database for writes by
	// other processes ("kickd event" and the other subcommands). The check
	// is one PRAGMA and costs little.
	pollEvery = 200 * time.Millisecond
	// fullScanEvery bounds the time between scans even without writes.
	fullScanEvery  = 5 * time.Second
	heartbeatEvery = 5 * time.Second
	pruneEvery     = time.Hour
)

// scheduleBatch is how many queued runs one scan reads. Tests shrink it.
var scheduleBatch = 500

// HeartbeatWindow is how old a heartbeat may be before "kickd status" reports that
// kickd is not running.
const HeartbeatWindow = 15 * time.Second

// Dispatcher consumes the SQLite queue. Every firing, whatever triggered
// it, is first a queued row; the loop then applies the concurrency policy
// of the event when it consumes the row.
type Dispatcher struct {
	r         *Runner
	store     *queue.Store
	log       *slog.Logger
	retention time.Duration

	mu         sync.Mutex
	specs      map[string]Spec
	running    map[string]int
	active     map[string]*activeRun // skip and queue policies: the run in progress
	cancels    map[int64]context.CancelCauseFunc
	cancelSent map[int64]bool
	runInfo    map[int64][2]string // run ID -> request ID, event
	waiters    map[int64]chan event.Result
	wake       chan struct{}

	limitMu    sync.Mutex
	errLimitAt map[string]time.Time
}

type activeRun struct {
	runID     int64
	requestID string
	start     time.Time
	skipped   int
}

// NewDispatcher creates a dispatcher. log carries the process requestId.
func NewDispatcher(r *Runner, store *queue.Store, log *slog.Logger, retention time.Duration) *Dispatcher {
	return &Dispatcher{
		r: r, store: store, log: log, retention: retention,
		specs:      map[string]Spec{},
		running:    map[string]int{},
		active:     map[string]*activeRun{},
		cancels:    map[int64]context.CancelCauseFunc{},
		cancelSent: map[int64]bool{},
		runInfo:    map[int64][2]string{},
		waiters:    map[int64]chan event.Result{},
		wake:       make(chan struct{}, 1),
		errLimitAt: map[string]time.Time{},
	}
}

// Configure replaces the event definitions. Running commands keep the
// definition they started with.
func (d *Dispatcher) Configure(specs []Spec) {
	m := make(map[string]Spec, len(specs))
	for _, s := range specs {
		m[s.Name] = s
	}
	d.mu.Lock()
	d.specs = m
	d.mu.Unlock()
	d.poke()
}

// Recover settles runs that the previous process left behind, following
// each event's on_interrupt: rerun queues the same firing again, up to
// max_attempts runs; abandon records it as abandoned. Call it once after
// Configure and before Loop.
func (d *Dispatcher) Recover(ctx context.Context) error {
	runs, err := d.store.InterruptedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		how := "agent_crashed"
		if run.Status == queue.StatusInterrupted {
			how = "agent_stopped"
		}
		log := d.log.With("requestId", run.RequestID, "event", run.Event, "runId", run.ID)
		d.mu.Lock()
		spec, ok := d.specs[run.Event]
		d.mu.Unlock()
		switch {
		case !ok:
			if err := d.store.Abandon(ctx, run.ID, "event_removed"); err != nil {
				return err
			}
			log.Warn("Interrupted run abandoned", "reason", "event_removed", "detail", how, "attempt", run.Attempt)
		case spec.OnInterrupt != InterruptRerun:
			if err := d.store.Abandon(ctx, run.ID, how); err != nil {
				return err
			}
			log.Warn("Interrupted run abandoned", "reason", how, "detail", "on_interrupt is abandon", "attempt", run.Attempt)
		case run.Attempt >= spec.MaxAttempts:
			if err := d.store.Abandon(ctx, run.ID, "max_attempts_reached"); err != nil {
				return err
			}
			log.Error("Interrupted run abandoned", "reason", "max_attempts_reached", "detail", how, "attempt", run.Attempt, "maxAttempts", spec.MaxAttempts)
		default:
			var ev event.Event
			if err := json.Unmarshal(run.Payload, &ev); err != nil {
				if err := d.store.Abandon(ctx, run.ID, "payload_invalid"); err != nil {
					return err
				}
				log.Error("Interrupted run abandoned", "reason", "payload_invalid", logging.Err(err))
				continue
			}
			ev.Attempt = run.Attempt + 1
			ev.RunID = 0
			payload, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			next, err := d.store.Retry(ctx, run, payload, how)
			if err != nil {
				return err
			}
			log.Warn("Interrupted run requeued", "reason", how, "attempt", ev.Attempt, "maxAttempts", spec.MaxAttempts, "detail", fmt.Sprintf("rerun as run %d", next))
		}
	}
	return nil
}

// Handler returns the event.Handler that the triggers of one event use.
func (d *Dispatcher) Handler(name string) event.Handler { return handler{d: d, name: name} }

type handler struct {
	d    *Dispatcher
	name string
}

func (h handler) Dispatch(ev event.Event) event.DispatchStatus {
	st, _, _, _ := h.d.submit(h.name, ev, false)
	return st
}

// RunSync queues the firing and waits for its run. When ctx ends first it
// returns ctx.Err() and the run continues.
func (h handler) RunSync(ctx context.Context, ev event.Event) (event.Result, error) {
	if ev.RequestID == "" {
		ev.RequestID = event.NewID()
	}
	st, _, wait, err := h.d.submit(h.name, ev, true)
	switch {
	case err != nil:
		return event.Result{}, err
	case st == event.Dropped:
		return event.Result{}, event.ErrQueueFull
	}
	select {
	case res := <-wait:
		switch res.Error {
		case "skipped":
			return event.Result{}, event.ErrBusy
		case "dropped":
			return event.Result{}, event.ErrQueueFull
		}
		return res, nil
	case <-ctx.Done():
		h.d.log.Info("Caller stopped waiting, run continues", "requestId", ev.RequestID, "event", h.name, "trigger", ev.Trigger, "reason", "client_disconnected")
		return event.Result{}, ctx.Err()
	}
}

// submit queues a firing of the named event. Policies apply when the loop
// consumes the row; only the backlog limit of the queue policy applies
// here, and a firing over it is recorded as dropped.
func (d *Dispatcher) submit(name string, ev event.Event, wait bool) (event.DispatchStatus, int64, chan event.Result, error) {
	if ev.RequestID == "" {
		ev.RequestID = event.NewID()
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	d.mu.Lock()
	spec, ok := d.specs[name]
	d.mu.Unlock()
	if !ok {
		return event.Dropped, 0, nil, fmt.Errorf("unknown event %q", name)
	}
	ev.Name = name
	ev.Data = WithDefaults(ev.Data, spec.Defaults)
	log := d.log.With("requestId", ev.RequestID, "event", name, "trigger", ev.Trigger)
	payload, err := json.Marshal(ev)
	if err != nil {
		return event.Dropped, 0, nil, err
	}
	max := 0
	if spec.Concurrency == PolicyQueue {
		max = MaxQueuedPerEvent
	}
	// Hold the lock across the insert so that the loop cannot consume the
	// row before the waiter is registered.
	d.mu.Lock()
	defer d.mu.Unlock()
	id, dropped, err := d.store.Enqueue(context.Background(), queue.Run{
		RequestID: ev.RequestID, Event: name, Trigger: ev.Trigger, TriggerID: ev.TriggerID, Source: ev.Source,
		Payload: payload, Attempt: 1, CreatedAt: ev.Time,
	}, max)
	if err != nil {
		d.queueError(log, "enqueue", err)
		return event.Dropped, 0, nil, err
	}
	if dropped {
		d.r.dropped.Add(1)
		log.Warn("Run dropped, queue full", "runId", id, "thresholdCount", max)
		return event.Dropped, id, nil, nil
	}
	var ch chan event.Result
	if wait {
		ch = make(chan event.Result, 1)
		d.waiters[id] = ch
	}
	log.Debug("Run queued", "runId", id)
	d.poke()
	return event.Queued, id, ch, nil
}

// WithDefaults returns data with the defaults filled in for missing keys.
func WithDefaults(data, defaults map[string]string) map[string]string {
	if len(defaults) == 0 {
		return data
	}
	out := make(map[string]string, len(data)+len(defaults))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range data {
		out[k] = v
	}
	return out
}

func (d *Dispatcher) poke() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Loop consumes the queue until ctx is done. info is written as the
// heartbeat that "kickd status" reads.
func (d *Dispatcher) Loop(ctx context.Context, info queue.Agent) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	d.heartbeat(ctx, info)
	d.prune(ctx)
	lastVersion, _ := d.store.DataVersion(ctx)
	lastScan, lastBeat, lastPrune := time.Now(), time.Now(), time.Now()
	d.step(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
			d.step(ctx)
		case now := <-tick.C:
			v, err := d.store.DataVersion(ctx)
			if err != nil {
				d.queueError(d.log, "data_version", err)
			}
			if v != lastVersion || now.Sub(lastScan) >= fullScanEvery {
				lastVersion, lastScan = v, now
				d.step(ctx)
			}
			if now.Sub(lastBeat) >= heartbeatEvery {
				d.heartbeat(ctx, info)
				lastBeat = now
			}
			if now.Sub(lastPrune) >= pruneEvery {
				d.prune(ctx)
				lastPrune = now
			}
		}
	}
}

func (d *Dispatcher) step(ctx context.Context) {
	if ctx.Err() != nil || d.r.ctx.Err() != nil {
		return
	}
	d.processCancels(ctx)
	d.schedule(ctx)
}

func (d *Dispatcher) processCancels(ctx context.Context) {
	ids, err := d.store.CancelRequests(ctx)
	if err != nil {
		d.queueError(d.log, "cancel_requests", err)
		return
	}
	for _, id := range ids {
		d.mu.Lock()
		cancel, ours := d.cancels[id]
		sent := d.cancelSent[id]
		info := d.runInfo[id]
		if ours && !sent {
			d.cancelSent[id] = true
		}
		d.mu.Unlock()
		switch {
		case ours && !sent:
			d.log.Info("Run cancel requested", "requestId", info[0], "event", info[1], "runId", id, "source", "cli")
			cancel(event.ErrCanceledByUser)
		case !ours:
			// A running row that no command of this process backs; it
			// cannot be stopped, so record the cancellation.
			if err := d.store.FinishRun(ctx, id, queue.Finish{Status: queue.StatusCanceled, Reason: "canceled_by_user"}); err != nil {
				d.queueError(d.log, "finish_run", err)
			}
		}
	}
}

// schedule consumes queued rows, oldest first. skip settles a row as
// skipped while its event runs; queue leaves it for later; parallel
// starts it at once.
//
// Rows of an event that runs under the queue policy cannot start yet, so
// the scan leaves them out: a long backlog of one event must not hide the
// rows of other events behind it. A scan that fills its batch and makes
// progress wakes the loop again, so that the rows after the batch start
// at once too.
func (d *Dispatcher) schedule(ctx context.Context) {
	d.mu.Lock()
	var waiting []string
	for name, n := range d.running {
		if n > 0 && d.specs[name].Concurrency == PolicyQueue {
			waiting = append(waiting, name)
		}
	}
	d.mu.Unlock()
	runs, err := d.store.QueuedRuns(ctx, scheduleBatch, waiting)
	if err != nil {
		d.queueError(d.log, "queued_runs", err)
		return
	}
	progressed := false
	defer func() {
		if progressed && len(runs) == scheduleBatch {
			d.poke()
		}
	}()
	for _, run := range runs {
		if d.r.ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		spec, ok := d.specs[run.Event]
		switch {
		case !ok:
			d.mu.Unlock()
			if done, ok := d.settle(ctx, run, queue.StatusDropped, "event_removed", ""); ok {
				d.log.Warn("Queued run dropped", "requestId", run.RequestID, "runId", run.ID, "event", run.Event, "reason", "event_removed")
				done()
			}
			progressed = true
			continue
		case spec.Concurrency == PolicySkip && d.running[run.Event] > 0:
			a := d.active[run.Event]
			d.mu.Unlock()
			d.skip(ctx, run, a)
			progressed = true
			continue
		case spec.Concurrency == PolicyQueue && d.running[run.Event] > 0:
			d.mu.Unlock()
			continue
		}
		started, err := d.store.StartRun(ctx, run.ID, time.Now())
		if err != nil || !started {
			d.mu.Unlock()
			if err != nil {
				d.queueError(d.log, "start_run", err)
			}
			continue
		}
		d.running[run.Event]++
		progressed = true
		var a *activeRun
		if spec.Concurrency != PolicyParallel {
			a = &activeRun{runID: run.ID, requestID: run.RequestID, start: time.Now()}
			d.active[run.Event] = a
		}
		runCtx, cancel := context.WithCancelCause(d.r.ctx)
		d.cancels[run.ID] = cancel
		d.runInfo[run.ID] = [2]string{run.RequestID, run.Event}
		d.mu.Unlock()

		var ev event.Event
		if err := json.Unmarshal(run.Payload, &ev); err != nil {
			d.log.Error("Queued run unreadable", "requestId", run.RequestID, "runId", run.ID, "event", run.Event, logging.Err(err))
			d.finish(run, spec, event.Result{ExitCode: -1, Error: "start_failed", Reason: "payload_invalid"})
			continue
		}
		ev.RequestID, ev.RunID, ev.Attempt = run.RequestID, run.ID, run.Attempt
		// Rows written by "kickd event" already carry defaults; fill them in for any
		// other writer too.
		ev.Data = WithDefaults(ev.Data, spec.Defaults)
		var summary func() []any
		if a != nil {
			summary = func() []any {
				d.mu.Lock()
				defer d.mu.Unlock()
				if a.skipped > 0 {
					return []any{"skipped", a.skipped}
				}
				return nil
			}
		}
		d.r.spawn(runCtx, spec, ev, summary, func(res event.Result) { d.finish(run, spec, res) })
	}
}

// skip settles a queued row as skipped because its event is running. The
// first skip per active run is logged at WARN and the rest at DEBUG; the
// active run's completion line and row carry the count.
func (d *Dispatcher) skip(ctx context.Context, run queue.Run, a *activeRun) {
	detail := ""
	if a != nil {
		detail = fmt.Sprintf("run %d", a.runID)
	}
	done, ok := d.settle(ctx, run, queue.StatusSkipped, "already_running", detail)
	if !ok {
		return
	}
	defer done()
	d.r.skipped.Add(1)
	log := d.log.With("requestId", run.RequestID, "event", run.Event, "trigger", run.Trigger, "runId", run.ID)
	if a == nil {
		log.Warn("Run skipped, previous run still active")
		return
	}
	d.mu.Lock()
	a.skipped++
	n := a.skipped
	d.mu.Unlock()
	if err := d.store.AddSkipped(ctx, a.runID); err != nil {
		d.queueError(log, "add_skipped", err)
	}
	attrs := []any{"activeRun", a.runID, "activeRequestId", a.requestID, "activeForMs", time.Since(a.start).Milliseconds()}
	if n == 1 {
		log.Warn("Run skipped, previous run still active", attrs...)
	} else {
		log.Debug("Run skipped, previous run still active", attrs...)
	}
}

// settle gives a queued row a final status without running it. It
// reports whether the row was still queued, and returns done, which tells
// a waiting webhook caller; call it after logging, so that the log line
// precedes the response.
func (d *Dispatcher) settle(ctx context.Context, run queue.Run, status, reason, detail string) (done func(), ok bool) {
	ok, err := d.store.Settle(ctx, run.ID, status, reason, detail)
	if err != nil {
		d.queueError(d.log, "settle", err)
		return func() {}, false
	}
	if !ok {
		return func() {}, false
	}
	d.mu.Lock()
	ch := d.waiters[run.ID]
	delete(d.waiters, run.ID)
	d.mu.Unlock()
	return func() {
		if ch != nil {
			ch <- event.Result{ExitCode: -1, Error: status, Reason: reason}
		}
	}, true
}

// finish records the result of a run and wakes the loop for the next one.
// A run cut off by shutdown is recorded as interrupted, to be recovered on
// the next start.
func (d *Dispatcher) finish(run queue.Run, spec Spec, res event.Result) {
	status := queue.StatusSucceeded
	switch {
	case res.Reason == "shutdown":
		status = queue.StatusInterrupted
	case res.Error == "canceled":
		status = queue.StatusCanceled
	case res.Error != "" || res.ExitCode != 0:
		status = queue.StatusFailed
	}
	output := ""
	if spec.LogOutput {
		output = res.Output
	}
	exit := res.ExitCode
	if err := d.store.FinishRun(context.Background(), run.ID, queue.Finish{
		Status: status, Reason: res.Reason, ExitCode: &exit, Signal: res.Signal,
		Output: output, OutputTruncated: res.Truncated, Duration: res.Duration,
	}); err != nil {
		d.queueError(d.log.With("requestId", run.RequestID, "runId", run.ID), "finish_run", err)
	}
	d.mu.Lock()
	d.running[run.Event]--
	if d.running[run.Event] <= 0 {
		delete(d.running, run.Event)
	}
	if a := d.active[run.Event]; a != nil && a.runID == run.ID {
		delete(d.active, run.Event)
	}
	if cancel := d.cancels[run.ID]; cancel != nil {
		cancel(nil)
	}
	delete(d.cancels, run.ID)
	delete(d.cancelSent, run.ID)
	delete(d.runInfo, run.ID)
	ch := d.waiters[run.ID]
	delete(d.waiters, run.ID)
	d.mu.Unlock()
	if ch != nil {
		ch <- res
	}
	d.poke()
}

func (d *Dispatcher) heartbeat(ctx context.Context, info queue.Agent) {
	if err := d.store.Heartbeat(ctx, info); err != nil {
		d.queueError(d.log, "heartbeat", err)
	}
}

func (d *Dispatcher) prune(ctx context.Context) {
	if d.retention <= 0 {
		return
	}
	n, err := d.store.Prune(ctx, time.Now().Add(-d.retention))
	if err != nil {
		d.queueError(d.log, "prune", err)
		return
	}
	if n > 0 {
		d.log.Debug("Queue pruned", "count", n, "retentionSec", int64(d.retention.Seconds()))
	}
}

// queueError logs a database failure at most once a minute per operation,
// so that a broken disk does not flood the log.
func (d *Dispatcher) queueError(log *slog.Logger, op string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	now := time.Now()
	d.limitMu.Lock()
	allowed := now.Sub(d.errLimitAt[op]) >= time.Minute
	if allowed {
		d.errLimitAt[op] = now
	}
	d.limitMu.Unlock()
	if allowed {
		log.Error("Queue operation failed", "detail", op, "file", d.store.Path(), logging.Err(err))
	}
}

// AgentInfo describes this process for the heartbeat.
func AgentInfo(version string, started time.Time) queue.Agent {
	host, _ := os.Hostname()
	return queue.Agent{PID: os.Getpid(), Version: version, Host: host, StartedAt: started}
}
