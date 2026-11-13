package agent

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/queue"
	"github.com/etak64n/kickd/internal/runner"
)

// fullQueue starts an agent whose event build, under concurrency: queue,
// runs one build while 1000 more wait, the most that an event keeps
// waiting. It returns the address of the webhook server, at which
// /hooks/build answers at once and /hooks/build-wait answers when the run
// ends.
func fullQueue(t *testing.T) string {
	t.Helper()
	addr := freeAddr(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "kickd.yaml")
	builds := filepath.Join(dir, "builds.txt")
	writeFile(t, cfgPath, "webhook:\n  listen: '"+addr+"'\nevents:\n  - name: build\n    concurrency: queue\n"+helperEvent("attempt", builds)+
		"    triggers:\n      - {type: webhook, path: /hooks/build}\n      - {type: webhook, path: /hooks/build-wait, wait: true}\n      - {type: manual}\n")
	store, err := queue.Open(filepath.Join(dir, "kickd.db"))
	if err != nil {
		t.Fatal(err)
	}
	for range runner.MaxQueuedPerEvent + 1 {
		kickEvent(t, store, "build", nil)
	}
	store.Close()

	log, listening := messageLogger("Webhook server listening", slog.LevelInfo)
	cancel, done := startAgentWith(t, cfgPath, log)
	t.Cleanup(func() { stopAgent(t, cancel, done) })
	select {
	case <-listening:
	case <-time.After(10 * time.Second):
		t.Fatal("the webhook server did not listen")
	}
	// The first build runs, for 30 seconds, so the other 1000 wait.
	waitForFile(t, builds, "attempt=1", 10*time.Second)
	return addr
}

// postForStatus sends a POST request to the webhook server at addr, and
// returns the status code and the status field of the answer.
func postForStatus(t *testing.T, addr, path string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Post("http://"+addr+path, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct{ Status string }
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("POST %s: %d, %v", path, res.StatusCode, err)
	}
	return res.StatusCode, body.Status
}

func TestAgentAnswers409ToAWebhookRequestWhoseRunIsDropped(t *testing.T) {
	addr := fullQueue(t)
	if code, status := postForStatus(t, addr, "/hooks/build"); code != http.StatusConflict || status != "dropped" {
		t.Errorf("POST /hooks/build: %d, status %q; want 409, dropped", code, status)
	}
}

func TestAgentAnswers409ToAWebhookRequestThatWaitsWhenItsRunIsDropped(t *testing.T) {
	addr := fullQueue(t)
	if code, status := postForStatus(t, addr, "/hooks/build-wait"); code != http.StatusConflict || status != "dropped" {
		t.Errorf("POST /hooks/build-wait: %d, status %q; want 409, dropped", code, status)
	}
}
