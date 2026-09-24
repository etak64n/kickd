// Package queue keeps every firing of a named event in a SQLite database.
// Triggers and the "kickd event" command add queued runs; the agent
// ("kickd run") consumes them.
// Runs survive restarts, and runs cut off by a stop are recovered on the
// next start according to the event's on_interrupt setting.
package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go driver: keeps cross-compilation free of cgo
)

// Run statuses. queued and running are open; interrupted waits for the
// next start to be recovered; the rest are final.
const (
	StatusQueued      = "queued"
	StatusRunning     = "running"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	StatusCanceled    = "canceled"
	StatusSkipped     = "skipped"
	StatusDropped     = "dropped"
	StatusInterrupted = "interrupted"
	StatusRetried     = "retried"
	StatusAbandoned   = "abandoned"
)

// Final reports whether a status will not change any more.
func Final(status string) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusInterrupted:
		return false
	}
	return true
}

// ErrNotFound is returned when a run does not exist.
var ErrNotFound = errors.New("not found")

const schemaVersion = 3

// schemaV2 creates the tables of version 2, the first released version.
var schemaV2 = []string{
	// Version 1 (never released) kept an inbox of named events.
	`DROP TABLE IF EXISTS events`,
	`DROP TABLE IF EXISTS runs`,
	`CREATE TABLE runs (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		request_id       TEXT    NOT NULL,
		event            TEXT    NOT NULL,
		trigger_kind     TEXT    NOT NULL,
		trigger_id       TEXT    NOT NULL,
		source           TEXT    NOT NULL DEFAULT '',
		payload          TEXT    NOT NULL,
		status           TEXT    NOT NULL,
		reason           TEXT    NOT NULL DEFAULT '',
		detail           TEXT    NOT NULL DEFAULT '',
		attempt          INTEGER NOT NULL DEFAULT 1,
		retry_of         INTEGER,
		exit_code        INTEGER,
		signal           TEXT    NOT NULL DEFAULT '',
		output           TEXT    NOT NULL DEFAULT '',
		output_truncated INTEGER NOT NULL DEFAULT 0,
		skipped          INTEGER NOT NULL DEFAULT 0,
		cancel_requested INTEGER NOT NULL DEFAULT 0,
		created_at       INTEGER NOT NULL,
		started_at       INTEGER,
		finished_at      INTEGER,
		duration_ms      INTEGER
	)`,
	`CREATE INDEX runs_status   ON runs (status, id)`,
	`CREATE INDEX runs_event    ON runs (event, status)`,
	`CREATE INDEX runs_request  ON runs (request_id)`,
	`CREATE INDEX runs_retry    ON runs (retry_of)`,
	`CREATE INDEX runs_finished ON runs (finished_at)`,
	`CREATE TABLE IF NOT EXISTS agent (
		id           INTEGER PRIMARY KEY CHECK (id = 1),
		pid          INTEGER NOT NULL,
		version      TEXT    NOT NULL,
		host         TEXT    NOT NULL,
		started_at   INTEGER NOT NULL,
		heartbeat_at INTEGER NOT NULL,
		stopped_at   INTEGER
	)`,
}

// schemaV3 adds the state of cron triggers: when kickd last handled each
// one, so that a start finds the scheduled times missed while stopped.
var schemaV3 = []string{
	`CREATE TABLE IF NOT EXISTS cron_state (
		trigger_key TEXT    PRIMARY KEY,
		last_at     INTEGER NOT NULL
	)`,
}

// Store is an open queue database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the database at path, creating it and its directory when
// needed, and upgrades its schema.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	// Create a new database file with owner-only permissions before SQLite
	// opens it. SQLite gives the -wal and -shm files that it creates next
	// to the database the permissions of the database file, and those
	// files hold recent payloads and command output as well.
	if f, err := os.OpenFile(abs, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	// Writes take the lock up front (_txlock=immediate) so that kickd and
	// the command-line subcommands never deadlock upgrading a read lock;
	// busy_timeout makes the
	// other side wait instead of failing.
	dsn := abs + "?_txlock=immediate" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection per process: statements of this process serialise,
	// and PRAGMA data_version then changes only for other processes.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: abs}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Two processes that open a new database at the same moment race to
	// switch it to WAL; the loser gets SQLITE_BUSY without waiting for the
	// busy timeout. Retry until the file is set up.
	for {
		err = s.migrate(ctx)
		if err == nil || !isBusy(err) || ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(20+rand.IntN(80)) * time.Millisecond)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("queue %s: %w", abs, err)
	}
	tightenSidecars(abs)
	return s, nil
}

// tightenSidecars narrows the permissions of the -wal and -shm files to
// those of the database file. kickd v0.1.0 set the permissions of a new
// database only after SQLite had created these files, so files that it
// left behind can be readable by other users.
func tightenSidecars(db string) {
	if runtime.GOOS == "windows" {
		return
	}
	st, err := os.Stat(db)
	if err != nil {
		return
	}
	want := st.Mode().Perm()
	for _, suffix := range []string{"-wal", "-shm"} {
		p := db + suffix
		if side, err := os.Stat(p); err == nil && side.Mode().Perm()&^want != 0 {
			_ = os.Chmod(p, want)
		}
	}
}

// isBusy reports whether err is SQLITE_BUSY (the database is locked).
func isBusy(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code()&0xff == 5 // SQLITE_BUSY and its extended codes
	}
	return strings.Contains(err.Error(), "SQLITE_BUSY")
}

// Path returns the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	switch {
	case v == schemaVersion:
		return nil
	case v > schemaVersion:
		return fmt.Errorf("schema version %d is newer than this program supports (%d)", v, schemaVersion)
	}
	// Each step upgrades from the version before it, so a database of an
	// earlier release keeps its runs.
	steps := []struct {
		version int
		stmts   []string
	}{{2, schemaV2}, {3, schemaV3}}
	for _, step := range steps {
		if v >= step.version {
			continue
		}
		for _, stmt := range step.stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// DataVersion changes whenever another process commits to the database.
func (s *Store) DataVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v)
	return v, err
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}

// Run is one firing of an event and its outcome.
type Run struct {
	ID              int64
	RequestID       string
	Event           string
	Trigger         string
	TriggerID       string
	Source          string
	Payload         []byte // the event.Event JSON the command receives
	Status          string
	Reason          string
	Detail          string
	Attempt         int
	RetryOf         int64
	ExitCode        *int
	Signal          string
	Output          string
	OutputTruncated bool
	Skipped         int
	CancelRequested bool
	CreatedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	Duration        time.Duration
}

// Open reports whether the run is still queued or running.
func (r Run) Open() bool { return r.Status == StatusQueued || r.Status == StatusRunning }

const runColumns = `id, request_id, event, trigger_kind, trigger_id, source, payload, status, reason, detail, attempt, retry_of,
	exit_code, signal, output, output_truncated, skipped, cancel_requested, created_at, started_at, finished_at, duration_ms`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var (
		r                        Run
		payload                  string
		retryOf, exit            sql.NullInt64
		truncated, cancel        int
		created                  int64
		started, finished, durMs sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.RequestID, &r.Event, &r.Trigger, &r.TriggerID, &r.Source, &payload, &r.Status, &r.Reason, &r.Detail,
		&r.Attempt, &retryOf, &exit, &r.Signal, &r.Output, &truncated, &r.Skipped, &cancel, &created, &started, &finished, &durMs); err != nil {
		return Run{}, err
	}
	r.Payload = []byte(payload)
	r.RetryOf = retryOf.Int64
	if exit.Valid {
		v := int(exit.Int64)
		r.ExitCode = &v
	}
	r.OutputTruncated = truncated != 0
	r.CancelRequested = cancel != 0
	r.CreatedAt = time.UnixMilli(created)
	r.StartedAt = fromMs(started)
	r.FinishedAt = fromMs(finished)
	if durMs.Valid {
		r.Duration = time.Duration(durMs.Int64) * time.Millisecond
	}
	return r, nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryRuns(ctx context.Context, q querier, query string, args ...any) ([]Run, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Enqueue adds a firing as a queued run. When maxQueued is positive and
// the event already has that many queued runs, the firing is recorded as
// dropped instead. It returns the run ID and whether it was dropped.
func (s *Store) Enqueue(ctx context.Context, r Run, maxQueued int) (int64, bool, error) {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if r.Attempt == 0 {
		r.Attempt = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	status, reason := StatusQueued, ""
	var finished any
	if maxQueued > 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE event = ? AND status = ?`, r.Event, StatusQueued).Scan(&n); err != nil {
			return 0, false, err
		}
		if n >= maxQueued {
			status, reason, finished = StatusDropped, "queue_full", ms(time.Now())
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO runs (request_id, event, trigger_kind, trigger_id, source, payload, status, reason, attempt,
		retry_of, created_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RequestID, r.Event, r.Trigger, r.TriggerID, r.Source, string(r.Payload), status, reason, r.Attempt,
		nullID(r.RetryOf), ms(r.CreatedAt), finished)
	if err != nil {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	return id, status == StatusDropped, tx.Commit()
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// QueuedRuns returns queued runs, oldest first, leaving out the runs of the
// events named in except.
func (s *Store) QueuedRuns(ctx context.Context, limit int, except []string) ([]Run, error) {
	q := `SELECT ` + runColumns + ` FROM runs WHERE status = ?`
	args := []any{StatusQueued}
	if len(except) > 0 {
		q += ` AND event NOT IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(except)), ", ") + `)`
		for _, e := range except {
			args = append(args, e)
		}
	}
	args = append(args, limit)
	return queryRuns(ctx, s.db, q+` ORDER BY id LIMIT ?`, args...)
}

// CountQueued returns the number of queued runs of an event.
func (s *Store) CountQueued(ctx context.Context, event string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE event = ? AND status = ?`, event, StatusQueued).Scan(&n)
	return n, err
}

// StartRun marks a queued run as running. It returns false when the run
// is no longer queued, for example because "kickd cancel" canceled it.
func (s *Store) StartRun(ctx context.Context, id int64, at time.Time) (bool, error) {
	return s.change(ctx, `UPDATE runs SET status = ?, started_at = ? WHERE id = ? AND status = ?`, StatusRunning, ms(at), id, StatusQueued)
}

func (s *Store) change(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Finish is the outcome written by FinishRun.
type Finish struct {
	Status          string
	Reason          string
	Detail          string
	ExitCode        *int
	Signal          string
	Output          string
	OutputTruncated bool
	At              time.Time
	Duration        time.Duration
}

// FinishRun records the outcome of a run.
func (s *Store) FinishRun(ctx context.Context, id int64, f Finish) error {
	if f.At.IsZero() {
		f.At = time.Now()
	}
	var exit any
	if f.ExitCode != nil {
		exit = *f.ExitCode
	}
	truncated := 0
	if f.OutputTruncated {
		truncated = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ?, reason = ?, detail = ?, exit_code = ?, signal = ?, output = ?,
		output_truncated = ?, finished_at = ?, duration_ms = ?, cancel_requested = 0 WHERE id = ?`,
		f.Status, f.Reason, f.Detail, exit, f.Signal, f.Output, truncated, ms(f.At), f.Duration.Milliseconds(), id)
	return err
}

// Settle gives a queued run a final status without running it (skipped,
// dropped). It returns false when the run was not queued any more.
func (s *Store) Settle(ctx context.Context, id int64, status, reason, detail string) (bool, error) {
	return s.change(ctx, `UPDATE runs SET status = ?, reason = ?, detail = ?, finished_at = ? WHERE id = ? AND status = ?`,
		status, reason, detail, ms(time.Now()), id, StatusQueued)
}

// AddSkipped counts a firing that was skipped because this run was active.
func (s *Store) AddSkipped(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET skipped = skipped + 1 WHERE id = ?`, id)
	return err
}

// RequestCancel cancels a queued run at once, or asks kickd to stop a
// running one. It returns the run as it is after the request.
func (s *Store) RequestCancel(ctx context.Context, id int64) (Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	switch r.Status {
	case StatusQueued:
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, reason = ?, finished_at = ? WHERE id = ?`,
			StatusCanceled, "canceled_by_user", ms(now), id); err != nil {
			return Run{}, err
		}
		r.Status, r.Reason, r.FinishedAt = StatusCanceled, "canceled_by_user", now
	case StatusRunning:
		if _, err := tx.ExecContext(ctx, `UPDATE runs SET cancel_requested = 1 WHERE id = ?`, id); err != nil {
			return Run{}, err
		}
		r.CancelRequested = true
	}
	return r, tx.Commit()
}

// CancelRequests returns running runs that "kickd cancel" asked to stop.
func (s *Store) CancelRequests(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM runs WHERE status = ? AND cancel_requested = 1`, StatusRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// InterruptedRuns returns runs cut off by the previous kickd process:
// rows still marked running after a crash, and rows marked interrupted
// by a clean stop. Call it before starting new runs.
func (s *Store) InterruptedRuns(ctx context.Context) ([]Run, error) {
	return queryRuns(ctx, s.db, `SELECT `+runColumns+` FROM runs WHERE status IN (?, ?) ORDER BY id`, StatusRunning, StatusInterrupted)
}

// Retry records an interrupted run as retried and queues the next attempt
// of the same firing. It returns the new run ID.
func (s *Store) Retry(ctx context.Context, r Run, payload []byte, reason string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO runs (request_id, event, trigger_kind, trigger_id, source, payload, status, attempt,
		retry_of, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RequestID, r.Event, r.Trigger, r.TriggerID, r.Source, string(payload), StatusQueued, r.Attempt+1, r.ID, ms(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, reason = ?, detail = ?, finished_at = COALESCE(finished_at, ?),
		cancel_requested = 0 WHERE id = ?`, StatusRetried, reason, fmt.Sprintf("rerun as run %d", id), ms(now), r.ID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// Abandon records an interrupted run as abandoned.
func (s *Store) Abandon(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ?, reason = ?, finished_at = COALESCE(finished_at, ?), cancel_requested = 0
		WHERE id = ?`, StatusAbandoned, reason, ms(time.Now()), id)
	return err
}

// RetryOf returns the run that repeats run id after an interruption.
func (s *Store) RetryOf(ctx context.Context, id int64) (Run, bool, error) {
	runs, err := queryRuns(ctx, s.db, `SELECT `+runColumns+` FROM runs WHERE retry_of = ? ORDER BY id LIMIT 1`, id)
	if err != nil || len(runs) == 0 {
		return Run{}, false, err
	}
	return runs[0], true, nil
}

// Filter selects runs for ListRuns.
type Filter struct {
	Event    string
	Statuses []string
	Limit    int
}

// ListRuns returns runs, newest first.
func (s *Store) ListRuns(ctx context.Context, f Filter) ([]Run, error) {
	var where []string
	var args []any
	if f.Event != "" {
		where = append(where, "event = ?")
		args = append(args, f.Event)
	}
	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(f.Statuses)), ", ")+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	q := `SELECT ` + runColumns + ` FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	return queryRuns(ctx, s.db, q, args...)
}

// GetRun returns one run.
func (s *Store) GetRun(ctx context.Context, id int64) (Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// RunsForRequest returns the runs of one firing, oldest first.
func (s *Store) RunsForRequest(ctx context.Context, requestID string) ([]Run, error) {
	return queryRuns(ctx, s.db, `SELECT `+runColumns+` FROM runs WHERE request_id = ? ORDER BY id`, requestID)
}

// Counts returns the number of queued and running runs.
func (s *Store) Counts(ctx context.Context) (queued, running int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) FROM runs WHERE status IN (?, ?)`,
		StatusQueued, StatusRunning, StatusQueued, StatusRunning).Scan(&queued, &running)
	return queued, running, err
}

// Prune deletes final runs older than before.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runs WHERE finished_at IS NOT NULL AND finished_at < ? AND status NOT IN (?, ?, ?)`,
		ms(before), StatusQueued, StatusRunning, StatusInterrupted)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Agent describes the kickd process that owns the queue.
type Agent struct {
	PID         int
	Version     string
	Host        string
	StartedAt   time.Time
	HeartbeatAt time.Time
	StoppedAt   time.Time
}

// Alive reports whether the agent wrote a heartbeat within the window.
func (a Agent) Alive(window time.Duration) bool {
	return a.StoppedAt.IsZero() && time.Since(a.HeartbeatAt) < window
}

// Heartbeat records that the agent is alive.
func (s *Store) Heartbeat(ctx context.Context, a Agent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent (id, pid, version, host, started_at, heartbeat_at, stopped_at)
		VALUES (1, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT (id) DO UPDATE SET pid = excluded.pid, version = excluded.version, host = excluded.host,
			started_at = excluded.started_at, heartbeat_at = excluded.heartbeat_at, stopped_at = NULL`,
		a.PID, a.Version, a.Host, ms(a.StartedAt), ms(time.Now()))
	return err
}

// AgentStopped records a clean shutdown.
func (s *Store) AgentStopped(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent SET stopped_at = ? WHERE id = 1`, ms(time.Now()))
	return err
}

// AgentInfo returns the last recorded agent, if any.
func (s *Store) AgentInfo(ctx context.Context) (Agent, bool, error) {
	var (
		a                  Agent
		started, heartbeat int64
		stopped            sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `SELECT pid, version, host, started_at, heartbeat_at, stopped_at FROM agent WHERE id = 1`).
		Scan(&a.PID, &a.Version, &a.Host, &started, &heartbeat, &stopped)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, false, nil
	}
	if err != nil {
		return Agent{}, false, err
	}
	a.StartedAt, a.HeartbeatAt, a.StoppedAt = time.UnixMilli(started), time.UnixMilli(heartbeat), fromMs(stopped)
	return a, true, nil
}

// LastCron returns when kickd last handled the cron trigger with key.
func (s *Store) LastCron(ctx context.Context, key string) (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT last_at FROM cron_state WHERE trigger_key = ?`, key).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(at), true, nil
}

// SetLastCron records when kickd last handled the cron trigger with key.
func (s *Store) SetLastCron(ctx context.Context, key string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cron_state (trigger_key, last_at) VALUES (?, ?)
		ON CONFLICT (trigger_key) DO UPDATE SET last_at = excluded.last_at`, key, ms(at))
	return err
}
