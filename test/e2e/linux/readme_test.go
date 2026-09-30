//go:build e2e && linux

package linux

import (
	"net/http"
	"os"
	"testing"
)

// The config of the README, as kickd init writes it for Linux, with the
// scripts of its events in ~/app. Its webhook listens on port 8787, so
// these tests run one at a time.

// readme returns a home with the README config and ~/app.
func readme(t *testing.T) *home {
	t.Helper()
	h := newHome(t, "readme")
	h.must("init")
	return h
}

func TestReadmeNotifyRunsByHand(t *testing.T) {
	h := readme(t)
	h.start()
	r := h.waitForRun(h.fire("notify"))
	if r.Status != "succeeded" || r.line("trigger") != "manual" || r.line("dir") != h.path("app") {
		t.Errorf("notify: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}

func TestReadmeDeployRunsOnAWebhookRequestWithTheToken(t *testing.T) {
	h := readme(t)
	h.start()
	res := request(t, "POST", 8787, "/hooks/deploy", "", "Authorization", "Bearer replace-with-a-long-random-string")
	if res.status != http.StatusAccepted {
		t.Fatalf("POST /hooks/deploy: %d %s", res.status, res.body)
	}
	r := h.waitForRuns("deploy", 1)[0]
	if r.Status != "succeeded" || r.line("trigger") != "webhook" || r.line("dir") != h.path("app") {
		t.Errorf("deploy: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}

func TestReadmeWebhookRefusesARequestWithoutTheToken(t *testing.T) {
	h := readme(t)
	h.start()
	if res := request(t, "POST", 8787, "/hooks/deploy", ""); res.status != http.StatusUnauthorized {
		t.Fatalf("POST /hooks/deploy without the token: %d %s", res.status, res.body)
	}
	if rs := h.runs("deploy"); len(rs) != 0 {
		t.Errorf("deploy ran: %v", rs)
	}
}

func TestReadmeBuildRunsWhenASourceFileChanges(t *testing.T) {
	h := readme(t)
	h.start()
	h.write("app/src/util.c", "int util(void) { return 1; }\n")
	r := h.waitForRuns("build", 1)[0]
	if r.Status != "succeeded" || r.line("trigger") != "file" || r.line("file") != h.path("app", "src", "util.c") {
		t.Errorf("build: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}

func TestReadmeNotifyRunsWhenTheBackupFails(t *testing.T) {
	h := readme(t)
	h.start()
	// ~/work does not exist, so rsync fails.
	if _, err := os.Stat(h.path("work")); err == nil {
		t.Fatal("~/work exists")
	}
	if r := h.waitForRun(h.fire("backup")); r.Status != "failed" {
		t.Fatalf("backup: %s, want failed\n%s", r.Status, r.Output)
	}
	r := h.waitForRuns("notify", 1)[0]
	if r.Status != "succeeded" || r.line("trigger") != "after" || r.line("after") != "backup" || r.line("status") != "failed" {
		t.Errorf("notify: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}
