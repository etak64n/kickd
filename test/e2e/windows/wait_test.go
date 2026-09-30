//go:build e2e && windows

package windows

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestEventWaitExitsWithZeroWhenTheRunSucceeds(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	h.start()
	if r := h.kickd("event", "backup", "--wait"); r.code != 0 {
		t.Errorf("kickd event backup --wait: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestEventWaitExitsWithOneWhenTheRunFails(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	h.start()
	if r := h.kickd("event", "verify", "--wait"); r.code != 1 {
		t.Errorf("kickd event verify --wait: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestEventWaitExitsWith124WhenTheTimeoutPasses(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	h.start()
	r := h.kickd("event", "restore", "--wait", "--timeout", "2s")
	if r.code != 124 || !strings.Contains(r.stderr, "timed out after 2s") {
		t.Errorf("kickd event restore --wait --timeout 2s: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

func TestEventWaitPrintsTheRunThatEnded(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	h.start()
	var rs []run
	if err := json.Unmarshal([]byte(h.must("event", "backup", "--wait", "--json")), &rs); err != nil || len(rs) != 1 {
		t.Fatalf("kickd event backup --wait --json: %v %v", rs, err)
	}
	if r := rs[0]; r.Event != "backup" || r.Status != "succeeded" || r.ExitCode == nil || *r.ExitCode != 0 {
		t.Errorf("the run: %+v", r)
	}
}

func TestEventWarnsWhenTheAgentIsNotRunning(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	if r := h.kickd("event", "backup"); r.code != 0 || !strings.Contains(r.stderr, "the agent is not running") {
		t.Errorf("kickd event backup: exit %d\n%s", r.code, r.stderr)
	}
}

func TestARunFiredWhileTheAgentIsNotRunningRunsWhenItStarts(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	id := h.fire("backup")
	h.start()
	if r := h.waitForRun(id); r.Status != "succeeded" {
		t.Errorf("backup: %s\n%s", r.Status, r.Output)
	}
}

func TestCancelCancelsARunThatHasNotStarted(t *testing.T) {
	t.Parallel()
	h := newHome(t, "wait")
	id := h.fire("backup")
	out := h.must("cancel", strconv.FormatInt(id, 10))
	if !strings.Contains(out, "it had not started") {
		t.Errorf("kickd cancel: %s", out)
	}
	if r := h.show(id); r.Status != "canceled" || r.StartedAt != "" {
		t.Errorf("backup: %s, started at %q", r.Status, r.StartedAt)
	}
}
