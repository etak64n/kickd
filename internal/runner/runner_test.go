package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

// The test binary doubles as the child process: when KICKD_TEST_HELPER is
// set it behaves as instructed by KICKD_HELPER_MODE and exits.
func TestMain(m *testing.M) {
	if os.Getenv("KICKD_TEST_HELPER") == "1" {
		helperMain()
		return
	}
	os.Exit(m.Run())
}

func helperMain() {
	switch os.Getenv("KICKD_HELPER_MODE") {
	case "env":
		for _, k := range []string{"KICKD_REQUEST_ID", "KICKD_EVENT", "KICKD_TRIGGER", "KICKD_TRIGGER_ID", "KICKD_ATTEMPT", "KICKD_FILE_PATH", "KICKD_FILE_OP", "KICKD_FILE_COUNT", "EXTRA"} {
			fmt.Printf("%s=%s\n", k, os.Getenv(k))
		}
		b, err := os.ReadFile(os.Getenv("KICKD_PAYLOAD_FILE"))
		if err != nil {
			fmt.Println("EVENT_FILE_ERROR=" + err.Error())
		}
		fmt.Println("EVENT_FILE=" + string(b))
		if os.Getenv("KICKD_HELPER_STDIN") == "1" {
			in, _ := io.ReadAll(os.Stdin)
			fmt.Println("STDIN=" + string(in))
		}
		wd, _ := os.Getwd()
		fmt.Println("CWD=" + wd)
		os.Exit(0)
	case "environ":
		// Every KICKD_ variable that kickd sets, empty ones included.
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "KICKD_") && !strings.HasPrefix(kv, "KICKD_TEST_") && !strings.HasPrefix(kv, "KICKD_HELPER_") {
				fmt.Println(kv)
			}
		}
		b, _ := os.ReadFile(os.Getenv("KICKD_PAYLOAD_FILE"))
		fmt.Println("EVENT_FILE=" + string(b))
		os.Exit(0)
	case "exit":
		fmt.Println("some stdout")
		fmt.Fprintln(os.Stderr, "first problem")
		fmt.Fprintln(os.Stderr, "failing on purpose token=abc123")
		os.Exit(3)
	case "sleep":
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "short":
		time.Sleep(700 * time.Millisecond)
		os.Exit(0)
	case "secret":
		fmt.Println("password=hunter2")
		os.Exit(0)
	case "orphan", "orphan-quiet":
		// Leave a child in the background; "orphan" hands it our output.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "KICKD_HELPER_MODE=sleep")
		if os.Getenv("KICKD_HELPER_MODE") == "orphan" {
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
		}
		if err := child.Start(); err != nil {
			fmt.Println("START_ERROR=" + err.Error())
			os.Exit(1)
		}
		fmt.Printf("CHILD=%d\n", child.Process.Pid)
		os.Exit(0)
	case "tree":
		// A child in the same process group that ignores SIGTERM.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "KICKD_HELPER_MODE=ignore-term")
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(os.Getenv("KICKD_HELPER_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(20 * time.Second)
		os.Exit(0)
	case "trap-term":
		// Exit with code 0 as soon as SIGTERM arrives.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		fmt.Println("ready")
		select {
		case <-ch:
			os.Exit(0)
		case <-time.After(20 * time.Second):
			os.Exit(1)
		}
	default:
		os.Exit(99)
	}
}

// recorder keeps log records so tests can assert on them.
type recorder struct {
	mu      sync.Mutex
	records []map[string]any
}

type recHandler struct {
	root  *recorder
	attrs []slog.Attr
}

func (h *recHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recHandler) WithGroup(string) slog.Handler            { return h }
func (h *recHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &recHandler{root: h.root, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *recHandler) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{"message": r.Message, "level": r.Level}
	add := func(a slog.Attr) {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			for _, g := range v.Group() {
				m[g.Key] = g.Value.Any()
			}
			return
		}
		m[a.Key] = v.Any()
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(a)
		return true
	})
	h.root.mu.Lock()
	h.root.records = append(h.root.records, m)
	h.root.mu.Unlock()
	return nil
}

func (r *recorder) find(message string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, m := range r.records {
		if m["message"] == message {
			out = append(out, m)
		}
	}
	return out
}

func (r *recorder) waitFor(t *testing.T, message string, n int, d time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if got := r.find(message); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q logged %d times, want %d", message, len(r.find(message)), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func helperJob(mode string, extra ...string) Spec {
	env := map[string]string{"KICKD_TEST_HELPER": "1", "KICKD_HELPER_MODE": mode}
	for i := 0; i+1 < len(extra); i += 2 {
		env[extra[i]] = extra[i+1]
	}
	return Spec{Name: "demo", Command: []string{os.Args[0]}, Env: env, Concurrency: PolicySkip, OnInterrupt: InterruptAbandon, MaxAttempts: 3, Stdin: StdinNone, LogOutput: true}
}

func newRunner(t *testing.T) (*Runner, *recorder, context.CancelFunc) {
	t.Helper()
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	r := New(ctx, slog.New(&recHandler{root: rec}), "proc")
	t.Cleanup(func() {
		cancel()
		r.Wait()
	})
	return r, rec, cancel
}

func fileEvent() event.Event {
	return event.Event{Name: "demo", Trigger: event.KindFile, TriggerID: "file:/w", Time: time.Now(),
		Files: []event.FileChange{{Path: "/w/a.txt", Op: "create"}, {Path: "/w/b.txt", Op: "write"}}}
}

func TestExecutePassesEventToCommand(t *testing.T) {
	r, rec, _ := newRunner(t)
	t.Setenv("KICKD_TEST_BASE", "1")
	job := helperJob("env", "EXTRA", "prefix-${KICKD_TEST_BASE}")
	job.Workdir = t.TempDir()
	ev := fileEvent()
	ev.RequestID = "req-42"
	res := r.Execute(context.Background(), job, ev)
	if res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{
		"KICKD_REQUEST_ID=req-42", "KICKD_EVENT=demo", "KICKD_TRIGGER=file", "KICKD_TRIGGER_ID=file:/w", "KICKD_ATTEMPT=1",
		"KICKD_FILE_PATH=/w/b.txt", "KICKD_FILE_OP=write", "KICKD_FILE_COUNT=2",
		"EXTRA=prefix-1", `"requestId":"req-42"`, `"event":"demo"`, `"op":"create"`,
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output lacks %q:\n%s", want, res.Output)
		}
	}
	if !strings.Contains(res.Output, "CWD="+job.Workdir) && !strings.Contains(res.Output, "CWD=/private"+job.Workdir) {
		t.Errorf("workdir not applied:\n%s", res.Output)
	}
	started, completed := rec.find("Run started"), rec.find("Run completed")
	if len(started) != 1 || len(completed) != 1 {
		t.Fatalf("started=%d completed=%d", len(started), len(completed))
	}
	if started[0]["requestId"] != "req-42" || completed[0]["requestId"] != "req-42" {
		t.Errorf("run lines must carry the event request ID: %v / %v", started[0], completed[0])
	}
	if started[0]["count"] != int64(2) || started[0]["file"] != "/w/b.txt" {
		t.Errorf("started = %v", started[0])
	}
	if completed[0]["exitCode"] != int64(0) {
		t.Errorf("exitCode = %v", completed[0]["exitCode"])
	}
	output := rec.find("Run output")
	if len(output) == 0 || output[0]["level"] != slog.LevelDebug || output[0]["text"] == nil {
		t.Errorf("output lines = %v", output)
	}
	if cmd := rec.find("Running command"); len(cmd) != 1 || cmd[0]["level"] != slog.LevelDebug {
		t.Errorf("Running command = %v", cmd)
	}
}

// A run gets every variable and payload key, also those of the other kinds
// of trigger, which are empty.
func TestExecuteGivesEveryRunTheSameVariables(t *testing.T) {
	r, _, _ := newRunner(t)
	ev := event.Event{Name: "demo", Trigger: event.KindManual, TriggerID: "manual", Time: time.Now(), Source: "alice@laptop"}
	res := r.Execute(context.Background(), helperJob("environ"), ev)
	if res.ExitCode != 0 || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	lines := strings.Split(strings.ReplaceAll(res.Output, "\r\n", "\n"), "\n")
	for _, want := range []string{
		"KICKD_DATA={}", "KICKD_MANUAL_SOURCE=alice@laptop", "KICKD_CRON_SCHEDULE=", "KICKD_CRON_MISSED=",
		"KICKD_WEBHOOK_PATH=", "KICKD_FILE_PATH=", "KICKD_FILE_COUNT=", "KICKD_FILE_PATHS=",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("environment lacks %q:\n%s", want, res.Output)
		}
	}
	for _, want := range []string{`"data":{}`, `"source":"alice@laptop"`, `"files":[]`, `"cron":null`, `"webhook":null`} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("payload lacks %s:\n%s", want, res.Output)
		}
	}
}

func TestExecuteGeneratesRequestID(t *testing.T) {
	r, rec, _ := newRunner(t)
	r.Execute(context.Background(), helperJob("env"), fileEvent())
	id, _ := rec.find("Run completed")[0]["requestId"].(string)
	if len(id) != 16 || id == "proc" {
		t.Errorf("generated requestId = %q", id)
	}
}

func TestExecuteStdinEvent(t *testing.T) {
	r, _, _ := newRunner(t)
	job := helperJob("env", "KICKD_HELPER_STDIN", "1")
	job.Stdin = StdinPayload
	res := r.Execute(context.Background(), job, fileEvent())
	if res.ExitCode != 0 || !strings.Contains(res.Output, `STDIN={"requestId":"`) {
		t.Fatalf("result = %+v", res)
	}
}

func TestExecuteFailureCarriesStderrTail(t *testing.T) {
	r, rec, _ := newRunner(t)
	res := r.Execute(context.Background(), helperJob("exit"), fileEvent())
	if res.ExitCode != 3 || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	failed := rec.find("Run failed")
	if len(failed) != 1 || failed[0]["reason"] != "exit_code" || failed[0]["level"] != slog.LevelError || failed[0]["exitCode"] != int64(3) {
		t.Fatalf("failed = %v", failed)
	}
	tail, _ := failed[0]["stderrTail"].(string)
	if tail != "first problem\nfailing on purpose token=***masked***" {
		t.Errorf("stderrTail = %q", tail)
	}
	if strings.Contains(tail, "some stdout") {
		t.Error("stderrTail must hold stderr only")
	}
}

func TestExecuteNoOutputLogging(t *testing.T) {
	r, rec, _ := newRunner(t)
	job := helperJob("exit")
	job.LogOutput = false
	r.Execute(context.Background(), job, fileEvent())
	if len(rec.find("Run output")) != 0 {
		t.Error("log_output=false must not log lines")
	}
	if failed := rec.find("Run failed"); len(failed) != 1 || failed[0]["stderrTail"] != nil {
		t.Errorf("log_output=false must not attach stderrTail: %v", failed)
	}
}

func TestExecuteTimeout(t *testing.T) {
	r, rec, _ := newRunner(t)
	job := helperJob("sleep")
	job.Timeout = 300 * time.Millisecond
	start := time.Now()
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Error != "timeout" {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatalf("timeout took %s", time.Since(start))
	}
	failed := rec.find("Run failed")
	if len(failed) != 1 || failed[0]["reason"] != "timeout" || failed[0]["thresholdMs"] != int64(300) {
		t.Fatalf("failed = %v", failed)
	}
	if runtime.GOOS != "windows" && failed[0]["signal"] != "SIGTERM" {
		t.Errorf("signal = %v", failed[0]["signal"])
	}
}

func TestExecuteCanceledOnShutdown(t *testing.T) {
	r, rec, cancel := newRunner(t)
	done := make(chan event.Result, 1)
	go func() { done <- r.Execute(context.Background(), helperJob("sleep"), fileEvent()) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if res.Error != "canceled" {
			t.Fatalf("result = %+v", res)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop after shutdown")
	}
	c := rec.find("Run interrupted")
	if len(c) != 1 || c[0]["level"] != slog.LevelWarn || c[0]["reason"] != "shutdown" {
		t.Errorf("canceled = %v", c)
	}
}

func TestExecuteStartFailure(t *testing.T) {
	r, rec, _ := newRunner(t)
	job := Spec{Name: "missing", Command: []string{"/definitely/not/here"}, LogOutput: true}
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Error != "start_failed" || res.ExitCode != -1 {
		t.Fatalf("result = %+v", res)
	}
	failed := rec.find("Run failed")
	if len(failed) != 1 || failed[0]["reason"] != "start_failed" || failed[0]["detail"] != "exec" {
		t.Fatalf("failed = %v", failed)
	}
	if typ, _ := failed[0]["errorType"].(string); typ == "" || failed[0]["errorMessage"] == nil {
		t.Errorf("error keys missing: %v", failed[0])
	}
}

// A bare program name is found in the PATH that the event gives its
// command, and not in the PATH of kickd.
func TestExecuteFindsProgramsInThePATHOfTheEvent(t *testing.T) {
	dir := t.TempDir()
	name := "kickd-helper-copy"
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, b, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _, _ := newRunner(t)
	job := helperJob("env", "PATH", dir)
	job.Command = []string{name}
	if res := r.Execute(context.Background(), job, fileEvent()); res.ExitCode != 0 {
		t.Fatalf("with the directory in the PATH of the event: %+v", res)
	}
	job = helperJob("env")
	job.Command = []string{name}
	if res := r.Execute(context.Background(), job, fileEvent()); res.Error != "start_failed" {
		t.Fatalf("without it: %+v", res)
	}
}

// A stop of kickd that comes before the process starts interrupts the run,
// so on_interrupt: rerun runs it again, instead of recording a failed start.
func TestExecuteCanceledBeforeStart(t *testing.T) {
	r, rec, _ := newRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := r.Execute(ctx, helperJob("env"), fileEvent())
	if res.Reason != "shutdown" || res.Error != "canceled" {
		t.Fatalf("result = %+v", res)
	}
	if n, f := len(rec.find("Run interrupted")), len(rec.find("Run failed")); n != 1 || f != 0 {
		t.Errorf("logged Run interrupted %d times and Run failed %d times", n, f)
	}
}

func TestExecuteShell(t *testing.T) {
	r, _, _ := newRunner(t)
	job := Spec{Name: "sh", Shell: "echo hello from shell", LogOutput: true}
	res := r.Execute(context.Background(), job, fileEvent())
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello from shell") {
		t.Fatalf("result = %+v", res)
	}
}

func TestCapBuffer(t *testing.T) {
	b := &capBuffer{limit: 5}
	b.Write([]byte("abc"))
	b.Write([]byte("defg"))
	b.Write([]byte("h"))
	if b.String() != "abcde" || !b.truncated {
		t.Fatalf("buf = %q truncated = %v", b.String(), b.truncated)
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{maxLines: 2, maxBytes: 1024}
	tb.Write([]byte("one\ntwo\r\nthr"))
	tb.Write([]byte("ee\nfour"))
	if got := tb.String(); got != "two\nthree\nfour" && got != "three\nfour" {
		t.Fatalf("tail = %q", got)
	}
	small := &tailBuffer{maxLines: 10, maxBytes: 5}
	small.Write([]byte("abcdefghij\n"))
	if got := small.String(); got != "fghij" {
		t.Fatalf("byte-limited tail = %q", got)
	}
}

func TestLineWriterSplitsLines(t *testing.T) {
	rec := &recorder{}
	w := &lineWriter{log: slog.New(&recHandler{root: rec}), stream: "stdout", capture: &capBuffer{limit: 100}, emit: true}
	w.Write([]byte("one\r\ntw"))
	w.Write([]byte("o\nthree"))
	w.flush()
	got := rec.find("Run output")
	if len(got) != 3 || got[0]["text"] != "one" || got[1]["text"] != "two" || got[2]["text"] != "three" || got[0]["stream"] != "stdout" {
		t.Fatalf("lines = %v", got)
	}
}

func TestExecuteKickdVariablesOverrideEnv(t *testing.T) {
	r, _, _ := newRunner(t)
	res := r.Execute(context.Background(), helperJob("env", "KICKD_EVENT", "spoofed"), fileEvent())
	if !strings.Contains(res.Output, "KICKD_EVENT=demo\n") || strings.Contains(res.Output, "spoofed") {
		t.Fatalf("env must not override KICKD_ variables:\n%s", res.Output)
	}
}

func TestExecuteStoredOutputIsNotMasked(t *testing.T) {
	r, rec, _ := newRunner(t)
	res := r.Execute(context.Background(), helperJob("secret"), fileEvent())
	if !strings.Contains(res.Output, "password=hunter2") {
		t.Fatalf("stored output must be raw: %q", res.Output)
	}
	lines := rec.find("Run output")
	if len(lines) != 1 || lines[0]["text"] != "password=***masked***" {
		t.Fatalf("the log line must be masked: %v", lines)
	}
}

// killChild stops the background child that the orphan helpers report.
func killChild(t *testing.T, output string) {
	t.Helper()
	m := regexp.MustCompile(`CHILD=(\d+)`).FindStringSubmatch(output)
	if m == nil {
		t.Fatalf("no child reported in %q", output)
	}
	pid, _ := strconv.Atoi(m[1])
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func shortGrace(t *testing.T) {
	old := killGrace
	killGrace = 300 * time.Millisecond
	t.Cleanup(func() { killGrace = old })
}

func TestExecuteBackgroundProcessHoldingOutput(t *testing.T) {
	shortGrace(t)
	r, rec, _ := newRunner(t)
	start := time.Now()
	res := r.Execute(context.Background(), helperJob("orphan"), fileEvent())
	killChild(t, res.Output)
	if res.ExitCode != 0 || res.Reason != "wait_failed" {
		t.Fatalf("a child holding the output must end the wait with wait_failed: %+v", res)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("waited %s for the output to close", d)
	}
	if f := rec.find("Run failed"); len(f) != 1 || f[0]["reason"] != "wait_failed" {
		t.Errorf("failed = %v", f)
	}
}

func TestExecuteBackgroundProcessWithClosedOutput(t *testing.T) {
	shortGrace(t)
	r, _, _ := newRunner(t)
	start := time.Now()
	res := r.Execute(context.Background(), helperJob("orphan-quiet"), fileEvent())
	killChild(t, res.Output)
	if res.ExitCode != 0 || res.Reason != "" || res.Error != "" {
		t.Fatalf("a child without our output must not affect the run: %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("run took %s", d)
	}
}
