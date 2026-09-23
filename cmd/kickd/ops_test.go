package main

import (
	"bytes"
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/agent"
	"github.com/etak64n/kickd/internal/config"
	"github.com/etak64n/kickd/internal/queue"
)

func ops(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runOps(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kickd.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestParseInterleavedFlags(t *testing.T) {
	fs := flag.NewFlagSet("event", flag.ContinueOnError)
	cfg := configFlag(fs)
	wait := fs.Bool("wait", false, "")
	timeout := fs.Duration("timeout", 0, "")
	pos, err := parse(fs, []string{"deploy", "--wait", "ref=main", "-c", "x.yaml", "--timeout=5s", "env=prod"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, " ") != "deploy ref=main env=prod" || !*wait || *cfg != "x.yaml" || *timeout != 5*time.Second {
		t.Fatalf("pos=%v wait=%v cfg=%q timeout=%s", pos, *wait, *cfg, *timeout)
	}
	if _, err := parse(fs, []string{"--nope"}); err == nil {
		t.Error("unknown flag must fail")
	}
	if _, err := parse(fs, []string{"-c"}); err == nil {
		t.Error("missing value must fail")
	}
}

func TestOpsWithoutAgent(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	code, out, errOut := ops(t, "event", "deploy", "-c", cfg, "ref=v2")
	if code != 0 || !regexp.MustCompile(`^queued run 1 \(event deploy, request [0-9a-f]{16}\)\n$`).MatchString(out) || !strings.Contains(errOut, "the agent is not running") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, _ := ops(t, "queue", "-c", cfg); code != 0 || !regexp.MustCompile(`1\s+deploy\s+manual\s+1\s+queued`).MatchString(out) {
		t.Errorf("queue: %d %q", code, out)
	}
	if code, out, _ := ops(t, "status", "-c", cfg); code != 0 || !strings.Contains(out, "agent: has not started") || !strings.Contains(out, "1 queued") {
		t.Errorf("status: %d %q", code, out)
	}
	if code, out, _ := ops(t, "show", "1", "-c", cfg); code != 0 || !strings.Contains(out, "ref=v2") || !strings.Contains(out, "attempt   1") {
		t.Errorf("show: %d %q", code, out)
	}
	if code, out, _ := ops(t, "cancel", "1", "-c", cfg); code != 0 || !strings.Contains(out, "had not started") {
		t.Errorf("cancel: %d %q", code, out)
	}
	if code, _, errOut := ops(t, "event", "nope", "-c", cfg); code != exitUsage || !strings.Contains(errOut, `"nope" is not defined`) || !strings.Contains(errOut, "deploy, boom, slow") {
		t.Errorf("unknown event: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, "event", "deploy", "colour=red", "-c", cfg); code != exitUsage || !strings.Contains(errOut, "unknown parameter colour") {
		t.Errorf("unknown param: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, "event", "deploy", "novalue", "-c", cfg); code != exitUsage || !strings.Contains(errOut, "KEY=VALUE") {
		t.Errorf("bad arg: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, "cancel", "99", "-c", cfg); code != exitFailed || !strings.Contains(errOut, "not found") {
		t.Errorf("cancel missing: %d %q", code, errOut)
	}
	if code, out, _ := ops(t, "events", "-c", cfg); code != 0 ||
		!regexp.MustCompile(`deploy\s+manual\s+skip\s+abandon\s+ref=main\s+Deploy the app`).MatchString(out) ||
		!regexp.MustCompile(`boom\s+manual, cron @yearly`).MatchString(out) ||
		!regexp.MustCompile(`slow\s+manual\s+skip\s+rerun \(max 3\)`).MatchString(out) {
		t.Errorf("events: %d %q", code, out)
	}
}

func startAgent(t *testing.T, cfg string) func() {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 4}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, logger, agent.Options{ConfigPath: cfg, Version: "test"}) }()
	waitAlive(t, filepath.Join(filepath.Dir(cfg), "kickd.db"))
	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestOpsWithAgent(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	stop := startAgent(t, cfg)
	defer stop()

	code, out, errOut := ops(t, "event", "deploy", "ref=v9", "--wait", "--timeout", "20s", "-c", cfg)
	if code != 0 || !regexp.MustCompile(`\d+\s+deploy\s+1\s+succeeded\s+0\s+`).MatchString(out) || errOut != "" {
		t.Fatalf("wait: code=%d out=%q err=%q", code, out, errOut)
	}
	id := regexp.MustCompile(`(?m)^(\d+)\s+deploy`).FindStringSubmatch(out)[1]
	if code, out, _ := ops(t, "show", id, "-c", cfg); code != 0 || !strings.Contains(out, "deploying v9") || !strings.Contains(out, "trigger   manual") {
		t.Errorf("show: %d %q", code, out)
	}
	if code, out, _ := ops(t, "event", "boom", "--wait", "--json", "-c", cfg); code != exitFailed || !strings.Contains(out, `"status": "failed"`) || !strings.Contains(out, `"exitCode": 7`) {
		t.Errorf("failing wait: %d %q", code, out)
	}
	if code, out, _ := ops(t, "runs", "--event", "boom", "-c", cfg); code != 0 || !strings.Contains(out, "boom") || strings.Contains(out, "deploy") {
		t.Errorf("runs --event: %d %q", code, out)
	}
	if code, out, _ := ops(t, "status", "-c", cfg); code != 0 || !strings.Contains(out, "agent: running") {
		t.Errorf("status: %d %q", code, out)
	}
}

// TestWaitFollowsRerun stops the agent while "kickd event --wait" waits for
// a rerun event and starts it again; the wait must follow the rerun and
// report its success.
func TestWaitFollowsRerun(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	stop := startAgent(t, cfg)
	result := make(chan [3]string, 1)
	go func() {
		code, out, errOut := ops(t, "event", "slow", "--wait", "--timeout", "40s", "-c", cfg)
		result <- [3]string{string(rune('0' + code)), out, errOut}
	}()
	db := filepath.Join(filepath.Dir(cfg), "kickd.db")
	waitStatusOf(t, db, "slow", queue.StatusRunning)
	stop()
	stop = startAgent(t, cfg)
	defer stop()
	select {
	case r := <-result:
		if r[0] != "0" || !regexp.MustCompile(`slow\s+1\s+retried`).MatchString(r[1]) || !regexp.MustCompile(`slow\s+2\s+succeeded`).MatchString(r[1]) {
			t.Fatalf("code=%s out=%q err=%q", r[0], r[1], r[2])
		}
	case <-time.After(45 * time.Second):
		t.Fatal("kickd event --wait did not return")
	}
}

func waitStatusOf(t *testing.T, db, name, status string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, err := queue.Open(db); err == nil {
			runs, _ := s.ListRuns(context.Background(), queue.Filter{Event: name, Statuses: []string{status}})
			s.Close()
			if len(runs) > 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s run of %s", status, name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitAlive(t *testing.T, dbPath string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, err := queue.Open(dbPath); err == nil {
			a, ok, _ := s.AgentInfo(context.Background())
			s.Close()
			if ok && a.Alive(15*time.Second) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("kickd did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEventDataFlag(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	if code, _, errOut := ops(t, "event", "deploy", "--data", `{"ref":"from-data"}`, "ref=from-arg", "-c", cfg); code != 0 {
		t.Fatalf("event: %d %q", code, errOut)
	}
	if code, out, _ := ops(t, "show", "1", "-c", cfg); code != 0 || !strings.Contains(out, "ref=from-arg") {
		t.Errorf("KEY=VALUE must override --data: %d %q", code, out)
	}
	if code, _, errOut := ops(t, "event", "deploy", "--data", "not json", "-c", cfg); code != exitUsage || !strings.Contains(errOut, "--data must be a JSON object of strings") {
		t.Errorf("bad --data: %d %q", code, errOut)
	}
}

func TestInitWritesTheExampleOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if err := cmdInit([]string{"-c", path}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != config.Example {
		t.Fatalf("init wrote %d bytes, err %v", len(b), err)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("config permissions %o, want 600", st.Mode().Perm())
	}
	if err := cmdInit([]string{"-c", path}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second init must refuse to overwrite: %v", err)
	}
}
