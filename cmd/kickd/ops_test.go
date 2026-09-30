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

func ops(t *testing.T, cfg string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runOps(cfg, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestParseInterleavedFlags(t *testing.T) {
	fs := flag.NewFlagSet("event", flag.ContinueOnError)
	data := fs.String("data", "", "")
	wait := fs.Bool("wait", false, "")
	timeout := fs.Duration("timeout", 0, "")
	pos, err := parse(fs, []string{"deploy", "--wait", "ref=main", "--data", "{}", "--timeout=5s", "env=prod"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, " ") != "deploy ref=main env=prod" || !*wait || *data != "{}" || *timeout != 5*time.Second {
		t.Fatalf("pos=%v wait=%v data=%q timeout=%s", pos, *wait, *data, *timeout)
	}
	if _, err := parse(fs, []string{"--nope"}); err == nil {
		t.Error("unknown flag must fail")
	}
	if _, err := parse(fs, []string{"--data"}); err == nil {
		t.Error("missing value must fail")
	}
}

// The flags of kickd v0.4 and earlier that chose the config file and the
// service fail with the reason.

func TestParseExplainsThatTheConfigFlagIsGone(t *testing.T) {
	fs := flag.NewFlagSet("event", flag.ContinueOnError)
	for arg, flag := range map[string]string{"-c": "-c", "--config": "--config", "--config=x.yaml": "--config"} {
		if _, err := parse(fs, []string{"deploy", arg, "x.yaml"}); err == nil || !strings.Contains(err.Error(), "kickd no longer takes "+flag+": it reads "+config.Path()) {
			t.Errorf("%s: %v", arg, err)
		}
	}
}

func TestNoArgsExplainsThatTheUserAndNameFlagsAreGone(t *testing.T) {
	if err := noArgs("service install", []string{"--user"}); err == nil || !strings.Contains(err.Error(), "kickd no longer takes --user") {
		t.Errorf("--user: %v", err)
	}
	if err := noArgs("run", []string{"--name", "kickd"}); err == nil || !strings.Contains(err.Error(), "kickd no longer takes --name") {
		t.Errorf("--name: %v", err)
	}
}

// The subcommands that kickd v0.5 and earlier had fail with what to run
// instead.

func TestRunsFailsWithTheNameOfHistory(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	if code, _, errOut := ops(t, cfg, "runs", "--event", "deploy"); code != exitUsage || !strings.Contains(errOut, "kickd runs is now kickd history") {
		t.Errorf("runs: %d %q", code, errOut)
	}
}

func TestQueueFailsWithTheHistoryOfTheRunsThatWait(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	if code, _, errOut := ops(t, cfg, "queue"); code != exitUsage || !strings.Contains(errOut, "kickd history --status queued") {
		t.Errorf("queue: %d %q", code, errOut)
	}
}

func TestNoArgsRefusesArguments(t *testing.T) {
	if err := noArgs("check", []string{"extra"}); err == nil || err.Error() != "kickd check takes no arguments: extra" {
		t.Errorf("noArgs: %v", err)
	}
}

func TestOpsWithoutAgent(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	code, out, errOut := ops(t, cfg, "event", "deploy", "ref=v2")
	if code != 0 || !regexp.MustCompile(`^queued run 1 \(event deploy, request [0-9a-f]{16}\)\n$`).MatchString(out) || !strings.Contains(errOut, "the agent is not running") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	if code, out, _ := ops(t, cfg, "history", "--status", "queued"); code != 0 || !regexp.MustCompile(`1\s+deploy\s+manual\s+queued`).MatchString(out) {
		t.Errorf("history: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "status"); code != 0 || !strings.Contains(out, "agent: has not started") || !strings.Contains(out, "1 queued") {
		t.Errorf("status: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "show", "1"); code != 0 || !strings.Contains(out, "ref=v2") || !strings.Contains(out, "attempt   1") {
		t.Errorf("show: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "cancel", "1"); code != 0 || !strings.Contains(out, "had not started") {
		t.Errorf("cancel: %d %q", code, out)
	}
	if code, _, errOut := ops(t, cfg, "event", "nope"); code != exitUsage || !strings.Contains(errOut, `"nope" is not defined`) || !strings.Contains(errOut, "deploy, boom, slow") {
		t.Errorf("unknown event: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, cfg, "event", "deploy", "colour=red"); code != exitUsage || !strings.Contains(errOut, "unknown parameter colour") {
		t.Errorf("unknown param: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, cfg, "event", "deploy", "novalue"); code != exitUsage || !strings.Contains(errOut, "KEY=VALUE") {
		t.Errorf("bad arg: %d %q", code, errOut)
	}
	// An event without a manual trigger cannot be fired by hand.
	if code, _, errOut := ops(t, cfg, "event", "nightly"); code != exitUsage || !strings.Contains(errOut, `has no manual trigger`) || !strings.Contains(errOut, `add "- type: manual"`) {
		t.Errorf("event without a manual trigger: %d %q", code, errOut)
	}
	if code, _, errOut := ops(t, cfg, "cancel", "99"); code != exitFailed || !strings.Contains(errOut, "not found") {
		t.Errorf("cancel missing: %d %q", code, errOut)
	}
	if code, out, _ := ops(t, cfg, "events"); code != 0 ||
		!regexp.MustCompile(`deploy\s+manual\s+skip\s+abandon\s+ref=main\s+config\.yaml\s+Deploy the app`).MatchString(out) ||
		!regexp.MustCompile(`boom\s+cron @yearly, manual`).MatchString(out) ||
		!regexp.MustCompile(`slow\s+manual\s+skip\s+rerun \(max 3\)`).MatchString(out) {
		t.Errorf("events: %d %q", code, out)
	}
}

// testLog passes log records to t.Log, which shows them when the test
// fails.
type testLog struct{ t *testing.T }

func (w testLog) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func startAgent(t *testing.T, cfg string) func() {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testLog{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
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

	code, out, errOut := ops(t, cfg, "event", "deploy", "ref=v9", "--wait", "--timeout", "20s")
	if code != 0 || !regexp.MustCompile(`\d+\s+deploy\s+1\s+succeeded\s+0\s+`).MatchString(out) || errOut != "" {
		t.Fatalf("wait: code=%d out=%q err=%q", code, out, errOut)
	}
	id := regexp.MustCompile(`(?m)^(\d+)\s+deploy`).FindStringSubmatch(out)[1]
	if code, out, _ := ops(t, cfg, "show", id); code != 0 || !strings.Contains(out, "deploying v9") || !strings.Contains(out, "trigger   manual") {
		t.Errorf("show: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "event", "boom", "--wait", "--json"); code != exitFailed || !strings.Contains(out, `"status": "failed"`) || !strings.Contains(out, `"exitCode": 7`) {
		t.Errorf("failing wait: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "history", "--event", "boom"); code != 0 || !strings.Contains(out, "boom") || strings.Contains(out, "deploy") {
		t.Errorf("history --event: %d %q", code, out)
	}
	if code, out, _ := ops(t, cfg, "status"); code != 0 || !strings.Contains(out, "agent: running") {
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
		code, out, errOut := ops(t, cfg, "event", "slow", "--wait", "--timeout", "40s")
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
	if code, _, errOut := ops(t, cfg, "event", "deploy", "--data", `{"ref":"from-data"}`, "ref=from-arg"); code != 0 {
		t.Fatalf("event: %d %q", code, errOut)
	}
	if code, out, _ := ops(t, cfg, "show", "1"); code != 0 || !strings.Contains(out, "ref=from-arg") {
		t.Errorf("KEY=VALUE must override --data: %d %q", code, out)
	}
	if code, _, errOut := ops(t, cfg, "event", "deploy", "--data", "not json"); code != exitUsage || !strings.Contains(errOut, "--data must be a JSON object of strings") {
		t.Errorf("bad --data: %d %q", code, errOut)
	}
}

func TestInitWritesTheExampleOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // the home directory on Windows
	// A config in the home directory is a user's; one outside it is for
	// the whole system, as /etc/kickd/config.yaml is.
	user := filepath.Join(home, ".kickd", "config.yaml")
	system := filepath.Join(t.TempDir(), "etc", "kickd", "config.yaml")
	for _, c := range []struct {
		path   string
		system bool
	}{{user, false}, {system, true}} {
		if err := cmdInit(c.path, c.system, nil); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(c.path); err != nil || string(b) != config.ExampleConfig(runtime.GOOS, c.system) {
			t.Fatalf("init wrote %d bytes to %s, err %v", len(b), c.path, err)
		}
		events := filepath.Join(filepath.Dir(c.path), config.ExampleEventsName)
		if b, err := os.ReadFile(events); err != nil || string(b) != config.ExampleEvents(runtime.GOOS) {
			t.Fatalf("init wrote %d bytes to %s, err %v", len(b), events, err)
		}
	}
	if !strings.Contains(config.ExampleConfig("linux", true), "'/var/lib/kickd/kickd.db'") || !strings.Contains(config.ExampleConfig("linux", false), "'~/.kickd/kickd.db'") {
		t.Fatal("the examples must name the database of a system and of a user")
	}
	for _, path := range []string{user, filepath.Join(filepath.Dir(user), config.ExampleEventsName)} {
		if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions %o, want 600", path, st.Mode().Perm())
		}
	}
	if err := cmdInit(user, false, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second init must refuse to overwrite: %v", err)
	}
}

func TestInitRefusesToOverwriteTheEventsFile(t *testing.T) {
	dir := t.TempDir()
	events := filepath.Join(dir, config.ExampleEventsName)
	if err := os.WriteFile(events, []byte("events: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdInit(filepath.Join(dir, "config.yaml"), false, nil); err == nil || !strings.Contains(err.Error(), events+" already exists") {
		t.Errorf("init over an events file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); err == nil {
		t.Error("init wrote the config file")
	}
}
