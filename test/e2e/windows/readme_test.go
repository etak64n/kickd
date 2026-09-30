//go:build e2e && windows

package windows

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// The config of the README, as kickd init writes it for Windows. Its
// events work in C:\scripts and C:\app, which the tests create from
// testdata/readme, so they change the machine and run only with
// KICKD_MACHINE_TEST=1. Its webhook listens on port 8787, so these tests
// run one at a time.

// readme returns a home with the README config, and C:\scripts and C:\app
// with the scripts of its events.
func readme(t *testing.T) *home {
	t.Helper()
	changesTheMachine(t)
	for _, dir := range []string{`C:\scripts`, `C:\app`} {
		if _, err := os.Stat(dir); err == nil {
			t.Fatalf("%s exists", dir)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	copyTree(t, `testdata\readme\scripts`, `C:\scripts`)
	copyTree(t, `testdata\readme\app`, `C:\app`)
	h := newHome(t, "")
	h.must("init")
	return h
}

func TestReadmeNotifyRunsByHand(t *testing.T) {
	h := readme(t)
	h.start()
	r := h.waitForRun(h.fire("notify"))
	if r.Status != "succeeded" || r.line("trigger") != "manual" || !strings.EqualFold(r.line("dir"), `C:\app`) {
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
	if r.Status != "succeeded" || r.line("trigger") != "webhook" || !strings.EqualFold(r.line("dir"), `C:\app`) {
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
	if err := os.WriteFile(`C:\app\src\util.c`, []byte("int util(void) { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := h.waitForRuns("build", 1)[0]
	if r.Status != "succeeded" || r.line("trigger") != "file" || !strings.EqualFold(r.line("file"), `C:\app\src\util.c`) {
		t.Errorf("build: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}

func TestReadmeNotifyRunsWhenTheBackupFails(t *testing.T) {
	h := readme(t)
	h.start()
	// C:\work does not exist, so robocopy fails.
	if _, err := os.Stat(`C:\work`); err == nil {
		t.Fatal(`C:\work exists`)
	}
	if r := h.waitForRun(h.fire("backup")); r.Status != "failed" {
		t.Fatalf("backup: %s, want failed\n%s", r.Status, r.Output)
	}
	r := h.waitForRuns("notify", 1)[0]
	if r.Status != "succeeded" || r.line("trigger") != "after" || r.line("after") != "backup" || r.line("status") != "failed" {
		t.Errorf("notify: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}
