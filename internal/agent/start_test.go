package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/queue"
)

// recordLogger keeps the messages of the records that it receives.
type recordLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordLogger) Enabled(context.Context, slog.Level) bool { return true }
func (r *recordLogger) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	r.msgs = append(r.msgs, rec.Message)
	r.mu.Unlock()
	return nil
}
func (r *recordLogger) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recordLogger) WithGroup(string) slog.Handler      { return r }

func (r *recordLogger) count(msg string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

func (r *recordLogger) waitFor(t *testing.T, msg string, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for r.count(msg) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%q was logged %d times, want %d", msg, r.count(msg), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func post(addr, path string) int {
	res, err := http.Post("http://"+addr+path+"?token=t", "text/plain", nil)
	if err != nil {
		return 0
	}
	res.Body.Close()
	return res.StatusCode
}

// When the address of the webhook server is in use, the agent stops
// before it starts any command, so no startup command runs and nothing is
// left running for the next start to take for a crash.
func TestAnAgentWhoseWebhookAddressIsInUseStartsNoCommand(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cfgPath := filepath.Join(dir, "kickd.yaml")
	writeFile(t, cfgPath, "webhook:\n  listen: '"+busy.Addr().String()+"'\nevents:\n"+
		"  - name: hook\n"+helperEvent("append", out, "HELPER_TEXT", "hook")+"    triggers: [{type: webhook, path: /hooks/hook, token: t}]\n"+
		"  - name: boot\n"+helperEvent("append", out, "HELPER_TEXT", "boot")+"    triggers: [{type: startup}]\n")
	_, done := startAgent(t, cfgPath)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the agent started on an address in use")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the agent did not stop")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a command ran: %v", err)
	}
	store, err := queue.Open(filepath.Join(dir, "kickd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if queued, running, err := store.Counts(context.Background()); err != nil || queued != 0 || running != 0 {
		t.Errorf("queued %d, running %d, %v", queued, running, err)
	}
}

// A second agent on the same database stops before it recovers runs, so
// the run of the first agent keeps running.
func TestASecondAgentOnTheSameDatabaseLeavesTheRunsOfTheFirst(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cfgPath := filepath.Join(dir, "kickd.yaml")
	writeFile(t, cfgPath, "events:\n  - name: sync\n    on_interrupt: rerun\n"+helperEvent("attempt", out)+"    triggers: [{type: startup}]\n")
	cancel, done := startAgent(t, cfgPath)
	defer stopAgent(t, cancel, done)
	waitForFile(t, out, "attempt=1", 15*time.Second)

	err := Run(context.Background(), quietLogger(), Options{ConfigPath: cfgPath, Version: "test"})
	var running *queue.AgentRunningError
	if !errors.As(err, &running) || running.PID != os.Getpid() {
		t.Fatalf("second agent: %v", err)
	}
	store, err := queue.Open(filepath.Join(dir, "kickd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, n, err := store.Counts(context.Background()); err != nil || n != 1 {
		t.Fatalf("running runs = %d, %v", n, err)
	}
}

// A saved config whose webhook server needs an address in use does not
// replace the running config: the old server keeps answering, and the new
// address takes effect once it is free and the config is saved again.
func TestSavingAConfigWhoseWebhookAddressIsInUseKeepsThePreviousConfig(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cfgPath := filepath.Join(dir, "kickd.yaml")
	config := func(addr string) string {
		return "webhook:\n  listen: '" + addr + "'\nevents:\n" +
			"  - name: hook\n" + helperEvent("append", out, "HELPER_TEXT", "hook") + "    triggers: [{type: webhook, path: /hooks/hook, token: t}]\n"
	}
	first := freeAddr(t)
	writeFile(t, cfgPath, config(first))
	rec := &recordLogger{}
	cancel, done := startAgentWith(t, cfgPath, slog.New(rec))
	defer stopAgent(t, cancel, done)
	rec.waitFor(t, "Webhook server listening", 1)

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfgPath, config(busy.Addr().String()))
	rec.waitFor(t, "Config reload failed", 1)
	if code := post(first, "/hooks/hook"); code != http.StatusAccepted {
		t.Fatalf("the previous webhook server answered %d", code)
	}
	if rec.count("Config reloaded") != 0 {
		t.Fatal("the config was reloaded")
	}

	second := busy.Addr().String()
	busy.Close()
	writeFile(t, cfgPath, config(second)+"\n")
	rec.waitFor(t, "Config reloaded", 1)
	rec.waitFor(t, "Webhook server listening", 2)
	if code := post(second, "/hooks/hook"); code != http.StatusAccepted {
		t.Fatalf("the new webhook server answered %d", code)
	}
}
