// Package event defines the payload of one firing of a named event and the
// handler interface that triggers hand firings to.
package event

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Trigger kinds: what fired the event.
const (
	KindManual  = "manual" // kickd event NAME
	KindCron    = "cron"
	KindWebhook = "webhook"
	KindFile    = "file"
)

// NewID returns a random 16 character hex ID for requestId.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000")
	}
	return hex.EncodeToString(b[:])
}

// ErrReload is the cancellation cause used when triggers stop for a
// configuration reload rather than for shutdown.
var ErrReload = errors.New("config reload")

// ErrCanceledByUser is the cancellation cause of a run stopped with
// "kickd cancel".
var ErrCanceledByUser = errors.New("canceled by user")

// Event is the payload of one firing of a named event. The command
// receives it as JSON and through environment variables.
type Event struct {
	// RequestID identifies the firing. A rerun after an interruption keeps
	// the request ID of the firing it repeats.
	RequestID string `json:"requestId"`
	// RunID is the row of the run in the queue database.
	RunID int64 `json:"runId,omitempty"`
	// Attempt counts runs of this firing, starting at 1; reruns after an
	// interruption increase it.
	Attempt   int               `json:"attempt,omitempty"`
	Name      string            `json:"event"`
	Trigger   string            `json:"trigger"`
	TriggerID string            `json:"triggerId"`
	Time      time.Time         `json:"time"`
	Data      map[string]string `json:"data,omitempty"`
	// Source is user@host of the "kickd event" command that fired the event.
	Source  string       `json:"source,omitempty"`
	Files   []FileChange `json:"files,omitempty"`
	Cron    *CronInfo    `json:"cron,omitempty"`
	Webhook *WebhookInfo `json:"webhook,omitempty"`
}

// FileChange is one file system change collected by a file trigger.
type FileChange struct {
	Path string `json:"path"`
	Op   string `json:"op"`
}

// CronInfo carries the schedule that fired.
type CronInfo struct {
	Schedule string `json:"schedule"`
}

// WebhookInfo carries the HTTP request that fired the event.
// Credentials (Authorization header, token header and query) are stripped.
type WebhookInfo struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	RemoteAddr string            `json:"remoteAddr"`
	Headers    map[string]string `json:"headers"`
	Query      map[string]string `json:"query"`
	Body       string            `json:"body"`
}

// Env returns the environment variables that describe the firing.
func (e Event) Env() []string {
	env := []string{
		"KICKD_REQUEST_ID=" + e.RequestID,
		"KICKD_EVENT=" + e.Name,
		"KICKD_TRIGGER=" + e.Trigger,
		"KICKD_TRIGGER_ID=" + e.TriggerID,
		"KICKD_TIME=" + e.Time.UTC().Format(time.RFC3339),
	}
	if e.RunID != 0 {
		env = append(env, "KICKD_RUN_ID="+strconv.FormatInt(e.RunID, 10))
	}
	attempt := e.Attempt
	if attempt == 0 {
		attempt = 1
	}
	env = append(env, "KICKD_ATTEMPT="+strconv.Itoa(attempt))
	data, _ := json.Marshal(e.Data)
	if e.Data == nil {
		data = []byte("{}")
	}
	env = append(env, "KICKD_EVENT_DATA="+string(data))
	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "KICKD_DATA_"+strings.ToUpper(k)+"="+e.Data[k])
	}
	if e.Source != "" {
		env = append(env, "KICKD_SOURCE="+e.Source)
	}
	if len(e.Files) > 0 {
		last := e.Files[len(e.Files)-1]
		paths := make([]string, len(e.Files))
		for i, f := range e.Files {
			paths[i] = f.Path
		}
		env = append(env,
			"KICKD_FILE_PATH="+last.Path,
			"KICKD_FILE_OP="+last.Op,
			"KICKD_FILE_COUNT="+strconv.Itoa(len(e.Files)),
			"KICKD_FILE_PATHS="+strings.Join(paths, string(os.PathListSeparator)),
		)
	}
	if e.Cron != nil {
		env = append(env, "KICKD_CRON_SCHEDULE="+e.Cron.Schedule)
	}
	if e.Webhook != nil {
		env = append(env,
			"KICKD_WEBHOOK_METHOD="+e.Webhook.Method,
			"KICKD_WEBHOOK_PATH="+e.Webhook.Path,
			"KICKD_WEBHOOK_REMOTE_ADDR="+e.Webhook.RemoteAddr,
		)
	}
	return env
}

// DispatchStatus tells what happened to a firing handed to the queue.
type DispatchStatus string

// Dispatch outcomes.
const (
	Queued  DispatchStatus = "queued"
	Dropped DispatchStatus = "dropped"
)

// ErrBusy is returned by RunSync when the run was skipped because the
// event was already running and its concurrency policy is "skip".
var ErrBusy = errors.New("event is already running")

// ErrQueueFull is returned by RunSync when the event's queue is full.
var ErrQueueFull = errors.New("event queue is full")

// Result is the outcome of one run.
type Result struct {
	ExitCode  int           `json:"exitCode"`
	Duration  time.Duration `json:"-"`
	Output    string        `json:"output"`
	Truncated bool          `json:"outputTruncated,omitempty"`
	// Error is a machine readable failure reason such as "timeout" or
	// "start_failed"; empty when the process ran to completion.
	Error string `json:"error,omitempty"`
	// Reason is the reason key of the log line: exit_code, timeout,
	// shutdown, canceled_by_user, start_failed, wait_failed or panic.
	Reason string `json:"-"`
	// Signal names the signal that ended the process, if any.
	Signal string `json:"-"`
}

// Handler receives firings from triggers.
type Handler interface {
	// Dispatch queues the firing without waiting.
	Dispatch(ev Event) DispatchStatus
	// RunSync queues the firing and waits for the result of its run.
	RunSync(ctx context.Context, ev Event) (Result, error)
}

// HandlerFunc adapts a plain function to Handler for internal triggers
// that never wait for a result.
type HandlerFunc func(ev Event)

// Dispatch calls f.
func (f HandlerFunc) Dispatch(ev Event) DispatchStatus {
	f(ev)
	return Queued
}

// RunSync calls f and returns an empty result.
func (f HandlerFunc) RunSync(_ context.Context, ev Event) (Result, error) {
	f(ev)
	return Result{}, nil
}
