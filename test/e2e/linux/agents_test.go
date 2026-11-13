//go:build e2e && linux

package linux

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// runAgentThatMustStop runs another "kickd run" in the home of h, which
// has to exit by itself, and returns its exit code and the records that it
// added to the log.
func runAgentThatMustStop(h *home) (int, string) {
	h.t.Helper()
	log := h.logFile()
	offset := fileSize(log)
	cmd := exec.Command(kickdPath, "run")
	cmd.Env, cmd.Dir = h.env, h.homeDir
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		<-done
		h.t.Fatal("kickd run did not stop")
	}
	b, _ := os.ReadFile(log)
	if int64(len(b)) < offset {
		offset = 0
	}
	return cmd.ProcessState.ExitCode(), string(b[offset:])
}

func TestASecondAgentDoesNotStartOnTheDatabaseOfARunningOne(t *testing.T) {
	t.Parallel()
	h := newHome(t, "runs")
	h.start()
	id := h.fire("sync")
	h.waitFor("sync runs", 30*time.Second, func() bool { return h.show(id).Status == "running" })
	code, logged := runAgentThatMustStop(h)
	if code == 0 || !strings.Contains(logged, "already runs on the database") {
		t.Fatalf("the second agent: exit %d\n%s", code, logged)
	}
	if r := h.waitForRun(id); r.Status != "succeeded" || r.Attempt != 1 {
		t.Errorf("the run of the first agent: %+v", r)
	}
}

func TestAnAgentWhoseWebhookAddressIsInUseStartsNoCommand(t *testing.T) {
	t.Parallel()
	busy, err := net.Listen("tcp", "127.0.0.1:18789")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	h := newHome(t, "address-in-use")
	code, logged := runAgentThatMustStop(h)
	if code == 0 || !strings.Contains(logged, "webhook listen") {
		t.Fatalf("kickd run: exit %d\n%s", code, logged)
	}
	if rs := h.runs("prepare"); len(rs) != 0 {
		t.Errorf("the startup event ran: %+v", rs)
	}
}

// The webhook server of testdata/webhook listens on port 18787, so this
// test runs with the other webhook tests, one at a time.
func TestWebhookRefusesARequestFromAWebBrowser(t *testing.T) {
	h := webhook(t)
	res := request(t, "POST", 18787, "/hooks/build", "", "Authorization", "Bearer webhook-token", "Origin", "https://example.com")
	if res.status != http.StatusForbidden {
		t.Fatalf("a request with Origin: %d %s", res.status, res.body)
	}
	if rs := h.runs("build"); len(rs) != 0 {
		t.Errorf("build ran: %+v", rs)
	}
}
