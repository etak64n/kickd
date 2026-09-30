//go:build e2e && windows

package windows

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests that start the agent of testdata/cli run one at a time,
// because its webhook listens on port 18788.

// hasLine reports whether text has the line want.
func hasLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimRight(line, "\r ") == want {
			return true
		}
	}
	return false
}

func TestCheckPrintsWhereTheLogAndTheDatabaseGo(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	out := h.must("check")
	for _, want := range []string{
		"OK: " + h.config() + " (3 events, 5 triggers)",
		"log: " + h.path("kickd.log") + " (level=info format=auto max_size_mb=10 max_backups=5)",
		"database: " + h.path("kickd.db") + " (retention 168h0m0s)",
		"webhook: listen=127.0.0.1:18788",
	} {
		if !hasLine(out, want) {
			t.Errorf("kickd check has no line %q:\n%s", want, out)
		}
	}
}

func TestCheckListsTheTriggersOfEachEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	out := h.must("check")
	for _, want := range []string{
		"- backup [skip; on interrupt: rerun, up to 3 attempts]: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'backup.ps1']",
		"    cron     0 3 * * * (Asia/Tokyo, missed=run)",
		"    manual   kickd event backup",
		"- deploy [queue; on interrupt: abandon]: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'deploy.ps1']",
		"    webhook  ANY /hooks/deploy (token)",
		"    manual   kickd event deploy",
		"    cron     0 2 * * * (missed=run)",
	} {
		if !hasLine(out, want) {
			t.Errorf("kickd check has no line %q:\n%s", want, out)
		}
	}
}

func TestCheckRejectsAnEventWithoutTriggers(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli-no-triggers")
	r := h.kickd("check")
	if r.code != 1 || !strings.Contains(r.stderr, `event "backup": triggers is required`) || !strings.Contains(r.stderr, "type: manual") {
		t.Errorf("kickd check: exit %d\n%s", r.code, r.stderr)
	}
}

func TestEventsListsTheEventsWithTheirTriggers(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	var events []struct {
		Name     string
		Triggers []string
	}
	if err := json.Unmarshal([]byte(h.must("events", "--json")), &events); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		got = append(got, e.Name+": "+strings.Join(e.Triggers, ", "))
	}
	want := "backup: cron 0 3 * * *, manual; deploy: webhook /hooks/deploy, manual; nightly: cron 0 2 * * *"
	if strings.Join(got, "; ") != want {
		t.Errorf("kickd events:\n%s\nwant\n%s", strings.Join(got, "; "), want)
	}
}

func TestEventRefusesAnEventWithoutAManualTrigger(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	r := h.kickd("event", "nightly")
	if r.code != 2 || !strings.Contains(r.stderr, `event "nightly" has no manual trigger`) || !strings.Contains(r.stderr, `add "- type: manual"`) {
		t.Errorf("kickd event nightly: exit %d\n%s", r.code, r.stderr)
	}
}

func TestInitDoesNotOverwriteAConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	if r := h.kickd("init"); r.code == 0 || !strings.Contains(r.stderr, "already exists") {
		t.Errorf("kickd init: exit %d\n%s", r.code, r.stderr)
	}
}

func TestStatusReportsTheRunningAgent(t *testing.T) {
	h := newHome(t, "cli")
	h.start()
	h.waitFor("kickd status reports the agent", 30*time.Second, func() bool {
		var st struct {
			Running bool
			Agent   struct{ PID int }
		}
		err := json.Unmarshal([]byte(h.must("status", "--json")), &st)
		return err == nil && st.Running && st.Agent.PID == h.agent.Process.Pid
	})
}

func TestHistoryListsTheRunsThatWait(t *testing.T) {
	h := newHome(t, "cli")
	h.start()
	for range 3 {
		h.fire("deploy")
	}
	h.waitFor("one deploy runs and two wait", 30*time.Second, func() bool {
		var q []run
		if err := json.Unmarshal([]byte(h.must("history", "--event", "deploy", "--json")), &q); err != nil {
			t.Fatal(err)
		}
		n := map[string]int{}
		for _, r := range q {
			if r.Event == "deploy" {
				n[r.Status]++
			}
		}
		return n["running"] == 1 && n["queued"] == 2
	})
}

func TestHistoryFiltersTheRunsByStatus(t *testing.T) {
	h := newHome(t, "cli")
	h.start()
	id := h.fire("backup")
	h.waitForRun(id)
	var succeeded, failed []run
	if err := json.Unmarshal([]byte(h.must("history", "--status", "succeeded", "--json")), &succeeded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range succeeded {
		found = found || r.ID == id
		if r.Status != "succeeded" {
			t.Errorf("kickd history --status succeeded lists run %d, which %s", r.ID, r.Status)
		}
	}
	if !found {
		t.Errorf("kickd history --status succeeded does not list run %d: %v", id, succeeded)
	}
	if err := json.Unmarshal([]byte(h.must("history", "--status", "failed", "--json")), &failed); err != nil || len(failed) != 0 {
		t.Errorf("kickd history --status failed: %v %v", failed, err)
	}
}

func TestShowPrintsTheOutputOfARun(t *testing.T) {
	h := newHome(t, "cli")
	h.start()
	id := h.fire("backup")
	h.waitForRun(id)
	out := h.must("show", strconv.FormatInt(id, 10))
	for _, want := range []string{"event     backup", "status    succeeded", "--- output ---", "backed up"} {
		if !hasLine(out, want) {
			t.Errorf("kickd show has no line %q:\n%s", want, out)
		}
	}
}

func TestVersionPrintsTheVersion(t *testing.T) {
	t.Parallel()
	h := newHome(t, "")
	if r := h.kickd("version"); r.code != 0 || !strings.HasPrefix(r.stdout, "kickd ") {
		t.Errorf("kickd version: exit %d, %q", r.code, r.stdout)
	}
}

func TestLicensesPrintTheLicenses(t *testing.T) {
	t.Parallel()
	h := newHome(t, "")
	if r := h.kickd("licenses"); r.code != 0 || !strings.Contains(r.stdout, "MIT License") {
		t.Errorf("kickd licenses: exit %d, %.200q", r.code, r.stdout)
	}
}

// eventNames returns the names of the events that kickd events lists.
func (h *home) eventNames() string {
	h.t.Helper()
	r := h.kickd("events", "--json")
	var events []struct{ Name string }
	if err := json.Unmarshal([]byte(r.stdout), &events); err != nil {
		h.t.Fatalf("kickd events: exit %d, %v\n%s", r.code, err, r.stderr)
	}
	var names []string
	for _, e := range events {
		names = append(names, e.Name)
	}
	return strings.Join(names, ", ")
}

func TestCommandsOfAnAdministratorReadTheConfigInProgramData(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	if out := h.must("check"); !strings.HasPrefix(out, "OK: "+h.inHome("kickd", "config.yaml")+" ") {
		t.Errorf("kickd check:\n%s", out)
	}
}

func TestCommandsOfAnAdministratorDoNotReadTheConfigInTheHomeFolder(t *testing.T) {
	t.Parallel()
	h := newHome(t, "config-admin")
	if got := h.eventNames(); got != "notify" {
		t.Errorf("kickd events lists %s, want notify", got)
	}
}

func TestCommandsDoNotReadKickdYamlInTheCurrentDirectory(t *testing.T) {
	t.Parallel()
	h := newHome(t, "config-cwd")
	if got := h.eventNames(); got != "notify" {
		t.Errorf("kickd events lists %s, want notify", got)
	}
}

func TestTheConfigFlagFailsWithTheReason(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	r := h.kickd("events", "-c", h.config())
	if r.code != 2 || !strings.Contains(r.stderr, "kickd no longer takes -c: it reads "+h.config()) {
		t.Errorf("kickd events -c: exit %d\n%s", r.code, r.stderr)
	}
}

func TestTheRunsCommandFailsWithTheReason(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	r := h.kickd("runs")
	if r.code != 2 || !strings.Contains(r.stderr, "kickd runs is now kickd history") {
		t.Errorf("kickd runs: exit %d\n%s", r.code, r.stderr)
	}
}

func TestTheQueueCommandFailsWithTheReason(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	r := h.kickd("queue")
	if r.code != 2 || !strings.Contains(r.stderr, "kickd history --status queued lists only the runs that wait") {
		t.Errorf("kickd queue: exit %d\n%s", r.code, r.stderr)
	}
}

func TestTheUserFlagFailsWithTheReason(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	r := h.kickd("service", "install", "--user")
	if r.code != 1 || !strings.Contains(r.stderr, "kickd no longer takes --user") {
		t.Errorf("kickd service install --user: exit %d\n%s", r.code, r.stderr)
	}
}

func TestInitOfAnAdministratorWritesTheConfigIntoProgramData(t *testing.T) {
	t.Parallel()
	h := newHome(t, "")
	r := h.kickd("init")
	want := h.path("config.yaml")
	if r.code != 0 || !hasLine(r.stdout, "wrote "+want) {
		t.Errorf("kickd init: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if fileSize(want) == 0 {
		t.Errorf("%s is empty or missing", want)
	}
}

func TestInitWritesAnEventsFileNextToTheConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "")
	r := h.kickd("init")
	want := h.path("event.example.yaml")
	if r.code != 0 || !hasLine(r.stdout, "wrote "+want) {
		t.Errorf("kickd init: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	if fileSize(want) == 0 {
		t.Errorf("%s is empty or missing", want)
	}
}

func TestInitOfAnAdministratorPutsTheLogAndTheDatabaseInProgramData(t *testing.T) {
	t.Parallel()
	h := newHome(t, "")
	out := h.must("init")
	for _, want := range []string{"log:      " + h.path("kickd.log"), "database: " + h.path("kickd.db")} {
		if !hasLine(out, want) {
			t.Errorf("kickd init has no line %q:\n%s", want, out)
		}
	}
}

func TestLogLevelInTheEnvironmentOverridesTheConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli")
	h.env = append(h.env, "LOG_LEVEL=debug")
	if out := h.must("check"); !strings.Contains(out, "(level=debug (from LOG_LEVEL) format=auto") {
		t.Errorf("kickd check:\n%s", out)
	}
}

func TestCheckSaysThatWebhooksAreOffWhenWebhookEnabledIsFalse(t *testing.T) {
	t.Parallel()
	h := newHome(t, "cli-webhook-off")
	if out := h.must("check"); !hasLine(out, "webhook: disabled by webhook.enabled, so webhook triggers do not fire") {
		t.Errorf("kickd check:\n%s", out)
	}
}
