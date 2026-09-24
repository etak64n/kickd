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
	// RunID is the row of the run in the database.
	RunID int64 `json:"runId"`
	// Attempt counts runs of this firing, starting at 1; reruns after an
	// interruption increase it.
	Attempt   int               `json:"attempt"`
	Name      string            `json:"event"`
	Trigger   string            `json:"trigger"`
	TriggerID string            `json:"triggerId"`
	Time      time.Time         `json:"time"`
	Data      map[string]string `json:"data"`
	// Source is user@host of the "kickd event" command that fired the event.
	Source  string       `json:"source"`
	Files   []FileChange `json:"files"`
	Cron    *CronInfo    `json:"cron"`
	Webhook *WebhookInfo `json:"webhook"`
}

// FileChange is one file system change collected by a file trigger.
type FileChange struct {
	Path string `json:"path"`
	Op   string `json:"op"`
}

// CronInfo carries the schedule that fired.
type CronInfo struct {
	Schedule string `json:"schedule"`
	// ScheduledAt is the scheduled time that the run stands for. When
	// several scheduled times passed at once, it is the latest of them.
	ScheduledAt time.Time `json:"scheduledAt"`
	// Missed reports that ScheduledAt passed while the machine slept or
	// kickd was stopped, and the run makes up for it.
	Missed bool `json:"missed"`
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

// Env returns the environment variables that describe the firing. Every
// firing gets the same names, whatever its event and its trigger: the
// variables of another kind of trigger are empty.
func (e Event) Env() []string {
	runID := ""
	if e.RunID != 0 {
		runID = strconv.FormatInt(e.RunID, 10)
	}
	attempt := e.Attempt
	if attempt == 0 {
		attempt = 1
	}
	data, _ := json.Marshal(e.Data)
	if e.Data == nil {
		data = []byte("{}")
	}
	env := []string{
		"KICKD_EVENT=" + e.Name,
		"KICKD_RUN_ID=" + runID,
		"KICKD_REQUEST_ID=" + e.RequestID,
		"KICKD_ATTEMPT=" + strconv.Itoa(attempt),
		"KICKD_TIME=" + e.Time.UTC().Format(time.RFC3339),
		"KICKD_TRIGGER=" + e.Trigger,
		"KICKD_TRIGGER_ID=" + e.TriggerID,
		"KICKD_DATA=" + string(data),
	}
	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "KICKD_DATA_"+strings.ToUpper(k)+"="+e.Data[k])
	}

	var cronSchedule, cronScheduledAt, cronMissed string
	if e.Cron != nil {
		cronSchedule, cronMissed = e.Cron.Schedule, "0"
		if !e.Cron.ScheduledAt.IsZero() {
			cronScheduledAt = e.Cron.ScheduledAt.UTC().Format(time.RFC3339)
		}
		if e.Cron.Missed {
			cronMissed = "1"
		}
	}
	var method, path, remoteAddr string
	if e.Webhook != nil {
		method, path, remoteAddr = e.Webhook.Method, e.Webhook.Path, e.Webhook.RemoteAddr
	}
	var filePath, fileOp, fileCount, filePaths string
	if len(e.Files) > 0 {
		last := e.Files[len(e.Files)-1]
		paths := make([]string, len(e.Files))
		for i, f := range e.Files {
			paths[i] = f.Path
		}
		filePath, fileOp, fileCount = last.Path, last.Op, strconv.Itoa(len(e.Files))
		filePaths = strings.Join(paths, string(os.PathListSeparator))
	}
	return append(env,
		"KICKD_MANUAL_SOURCE="+e.Source,
		"KICKD_CRON_SCHEDULE="+cronSchedule,
		"KICKD_CRON_SCHEDULED_AT="+cronScheduledAt,
		"KICKD_CRON_MISSED="+cronMissed,
		"KICKD_WEBHOOK_METHOD="+method,
		"KICKD_WEBHOOK_PATH="+path,
		"KICKD_WEBHOOK_REMOTE_ADDR="+remoteAddr,
		"KICKD_FILE_PATH="+filePath,
		"KICKD_FILE_OP="+fileOp,
		"KICKD_FILE_COUNT="+fileCount,
		"KICKD_FILE_PATHS="+filePaths,
	)
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
