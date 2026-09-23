package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T, o Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	o.Stderr = &buf
	logger, closer, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closer.Close() })
	return logger, &buf
}

func lines(buf *bytes.Buffer) []string {
	s := strings.TrimSpace(buf.String())
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestJSONKeyOrder(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Level: "info", Format: "json"})
	logger.With("requestId", "abc123").Info("Job completed", "job", "sync", "exitCode", 0, "durationMs", 12)
	got := lines(buf)
	if len(got) != 1 {
		t.Fatalf("lines = %q", got)
	}
	re := regexp.MustCompile(`^\{"timestamp":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z","level":"INFO","message":"Job completed","requestId":"abc123","service":"kickd","job":"sync","exitCode":0,"durationMs":12\}$`)
	if !re.MatchString(got[0]) {
		t.Fatalf("line = %s", got[0])
	}
}

func TestAutoFormatIsJSONWhenNotATerminal(t *testing.T) {
	logger, buf := newTestLogger(t, Options{})
	logger.Info("Agent started")
	if !strings.HasPrefix(buf.String(), `{"timestamp":`) {
		t.Fatalf("auto format to a buffer must be JSON: %q", buf.String())
	}
}

func TestLevels(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Level: "trace", Format: "json"})
	logger.Log(context.Background(), LevelTrace, "a")
	logger.Debug("b")
	logger.Info("c")
	logger.Warn("d")
	logger.Error("e")
	logger.Log(context.Background(), LevelFatal, "f")
	var names []string
	for _, l := range lines(buf) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		names = append(names, m["level"].(string))
	}
	if got := strings.Join(names, ","); got != "TRACE,DEBUG,INFO,WARN,ERROR,FATAL" {
		t.Fatalf("levels = %s", got)
	}

	logger, buf = newTestLogger(t, Options{Level: "info", Format: "json"})
	logger.Log(context.Background(), LevelTrace, "hidden")
	logger.Debug("hidden")
	logger.Info("shown")
	if n := len(lines(buf)); n != 1 {
		t.Fatalf("info level wrote %d lines", n)
	}
	for _, name := range []string{"trace", "DEBUG", "info", "warn", "warning", "error", "fatal"} {
		if _, ok := ParseLevel(name); !ok {
			t.Errorf("ParseLevel(%q) failed", name)
		}
	}
	if _, ok := ParseLevel("verbose"); ok {
		t.Error("ParseLevel(verbose) should fail")
	}
}

func TestErrorLinesCarryLocation(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Format: "json"})
	logger.Info("No location on INFO")
	logger.Error("Config parse failed", Err(fmt.Errorf("read: %w", &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist})))
	got := lines(buf)
	var info, failed map[string]any
	json.Unmarshal([]byte(got[0]), &info)
	json.Unmarshal([]byte(got[1]), &failed)
	if _, ok := info["location"]; ok {
		t.Error("INFO must not carry location")
	}
	loc, _ := failed["location"].(string)
	if !regexp.MustCompile(`^logging_test\.go:TestErrorLinesCarryLocation:\d+$`).MatchString(loc) {
		t.Errorf("location = %q", loc)
	}
	if failed["errorType"] != "fs.PathError" || failed["errorMessage"] != "read: open /x: file does not exist" {
		t.Errorf("error keys = %v", failed)
	}
}

func TestErrorType(t *testing.T) {
	pathErr := &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist}
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("plain"), "errors.errorString"},
		{pathErr, "fs.PathError"},
		{fmt.Errorf("wrapped: %w", pathErr), "fs.PathError"},
		{fmt.Errorf("two: %w, %w", pathErr, errors.New("b")), "fs.PathError"},
		{errors.Join(pathErr, errors.New("b")), "fs.PathError"},
		{fmt.Errorf("no wrap %v", 1), "fmt.wrapError"},
	}
	for _, c := range cases {
		got := ErrorType(c.err)
		if c.want == "fmt.wrapError" {
			// fmt.Errorf without %w returns *errors.errorString.
			c.want = "errors.errorString"
		}
		if got != c.want {
			t.Errorf("ErrorType(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestPanicAttrs(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Format: "json"})
	func() {
		defer func() {
			if p := recover(); p != nil {
				logger.Error("Job failed", Panic(p, debug.Stack()))
			}
		}()
		var m map[string]int
		m["boom"] = 1
	}()
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines(buf)[0]), &rec); err != nil {
		t.Fatal(err)
	}
	frames, _ := rec["stackTrace"].([]any)
	if len(frames) == 0 {
		t.Fatalf("no stackTrace: %v", rec)
	}
	first, _ := frames[0].(string)
	if !strings.Contains(first, "TestPanicAttrs") || !strings.Contains(first, "logging_test.go:") {
		t.Errorf("first frame = %q", first)
	}
	if rec["errorType"] != "runtime.plainError" && rec["errorType"] != "runtime.Error" && !strings.HasPrefix(rec["errorType"].(string), "runtime.") {
		t.Errorf("errorType = %v", rec["errorType"])
	}
	if !strings.Contains(rec["errorMessage"].(string), "nil map") {
		t.Errorf("errorMessage = %v", rec["errorMessage"])
	}
}

func TestTextFormat(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Level: "debug", Format: "text"})
	logger.Warn("Config\twarning\nnext", "detail", "x\ty", "job", "a")
	logger.With("requestId", "r1").Info("Agent started")
	got := lines(buf)
	if len(got) != 2 {
		t.Fatalf("lines = %q", got)
	}
	cols := strings.Split(got[0], "\t")
	if len(cols) != 5 || cols[1] != "-" || cols[2] != "WARN" || cols[3] != "Config warning next" || cols[4] != `{"detail":"x\ty","job":"a"}` {
		t.Errorf("columns = %q", cols)
	}
	cols = strings.Split(got[1], "\t")
	if len(cols) != 5 || cols[1] != "r1" || cols[2] != "INFO" || cols[4] != "-" {
		t.Errorf("columns = %q", cols)
	}
	if strings.Contains(buf.String(), "service") {
		t.Error("text format must not print service")
	}
}

func TestRequestIDAndDuplicateKeys(t *testing.T) {
	logger, buf := newTestLogger(t, Options{Format: "json"})
	logger.With("requestId", "proc", "job", "a").Info("Job skipped", "requestId", "run", "job", "b")
	var m map[string]any
	json.Unmarshal([]byte(lines(buf)[0]), &m)
	if m["requestId"] != "run" || m["job"] != "b" {
		t.Errorf("record = %v", m)
	}
	if strings.Count(buf.String(), `"job"`) != 1 {
		t.Errorf("duplicate key written: %s", buf.String())
	}
}

func TestColorOnlyWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(&buf, slog.LevelInfo, FormatText, true)
	slog.New(h).Error("Job failed")
	slog.New(h).Info("Job completed")
	out := buf.String()
	if !strings.HasPrefix(out, "\033[0;31m") || !strings.Contains(out, "\033[0m\n\033[0;36m") {
		t.Errorf("colors missing: %q", out)
	}
	buf.Reset()
	slog.New(newHandler(&buf, slog.LevelInfo, FormatText, false)).Error("Job failed")
	if strings.Contains(buf.String(), "\033[") {
		t.Errorf("escape codes without color: %q", buf.String())
	}
}

func TestConsoleIsSkippedWithoutTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kickd.log")
	var stderr bytes.Buffer
	logger, closer, err := New(Options{File: path, Console: true, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("Agent started")
	closer.Close()
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"message":"Agent started"`) {
		t.Errorf("file = %q", data)
	}
	if stderr.Len() != 0 {
		t.Errorf("console output without a terminal: %q", stderr.String())
	}
}

func TestFanout(t *testing.T) {
	var a, b bytes.Buffer
	h := fanout{newHandler(&a, slog.LevelInfo, FormatJSON, false), newHandler(&b, slog.LevelInfo, FormatText, false)}
	slog.New(h).With("requestId", "x").Info("Agent started", "version", "1")
	if !strings.Contains(a.String(), `"requestId":"x"`) || !strings.Contains(b.String(), "\tx\tINFO\tAgent started\t{\"version\":\"1\"}") {
		t.Errorf("json = %q, text = %q", a.String(), b.String())
	}
}

func TestRotationKeepsGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.log")
	rf, err := openRotating(path, 100, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strings.Repeat("x", 59) + "\n")
	for i := 0; i < 5; i++ {
		if _, err := rf.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	rf.Close()
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s missing: %v", filepath.Base(name), err)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(".3 must not exist: %v", err)
	}
}

func TestRotationFailureIsReported(t *testing.T) {
	var report bytes.Buffer
	r := &rotatingFile{report: &report, path: "/x/kickd.log"}
	r.reportFailure(errors.New("disk full"))
	cols := strings.Split(strings.TrimSpace(report.String()), "\t")
	if len(cols) != 5 || cols[2] != "WARN" || cols[3] != "Log rotation failed" || !strings.Contains(cols[4], `"errorMessage":"disk full"`) {
		t.Errorf("report = %q", report.String())
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		`curl -H "Authorization: Bearer abc.def" https://x`: `curl -H "Authorization: Bearer ***masked***" https://x`,
		`token=s3cr3t&x=1`:                       `token=***masked***&x=1`,
		`mysql --password hunter2 db`:            `mysql --password ***masked*** db`,
		`API_KEY: sk-12345`:                      `API_KEY: ***masked***`,
		`https://user:pa55@example.com/repo.git`: `https://user:***masked***@example.com/repo.git`,
		`{"secret":"v"}`:                         `{"secret":"***masked***"}`,
		`nothing to hide, tokens: 5`:             `nothing to hide, tokens: 5`,
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSignalName(t *testing.T) {
	if got := SignalName(os.Interrupt); got != "SIGINT" {
		t.Errorf("SignalName(Interrupt) = %q", got)
	}
}
