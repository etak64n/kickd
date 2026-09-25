//go:build usecase

package usecase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The subcommands that show the setup and the runs, and the rules of the
// triggers: only an event with a manual trigger can be fired by hand, and
// an event without triggers is an error.
func TestUseCaseCLI(t *testing.T) {
	t.Parallel()
	// Without its manual trigger, build fires only on file changes.
	h := setup(t, edit{"      - type: manual         # kickd event build also runs it\n", ""})
	src := filepath.Join(h.app, "src")

	// kickd check validates the config and shows where everything goes.
	out := h.must("check")
	for _, want := range []string{
		"OK: " + h.cfg + " (4 events, 6 triggers)",
		"log: " + h.log + " (level=info format=auto max_size_mb=10 max_backups=5)",
		"database: " + h.db + " (retention 168h0m0s)",
		"webhook: listen=127.0.0.1:" + h.port,
		"    workdir  " + h.backup,
		"    workdir  " + h.app,
		"    cron     0 3 * * * (Asia/Tokyo, missed=run)",
		"    webhook  POST /hooks/deploy (token)",
		"    file     " + src + " (changes=create,write,remove,rename, debounce=2s, recursive)",
		"    manual   kickd event backup",
		"    manual   kickd event deploy",
		"    manual   kickd event notify",
	} {
		if !containsLine(out, want) {
			t.Errorf("kickd check has no line %q:\n%s", want, out)
		}
	}
	for _, want := range []string{
		`^- backup \[skip; on interrupt: rerun, up to 3 attempts\]: `,
		`^- deploy \[queue; on interrupt: abandon\]: `,
		`^- build \[queue; on interrupt: abandon\]: `,
		`^- notify \[parallel; on interrupt: abandon\]: `,
	} {
		if !regexp.MustCompile("(?m)" + want).MatchString(out) {
			t.Errorf("kickd check has no line that matches %s:\n%s", want, out)
		}
	}
	if containsLine(out, "    manual   kickd event build") {
		t.Errorf("kickd check shows a manual trigger of build:\n%s", out)
	}

	// kickd events lists the events with their triggers in order.
	var events []struct {
		Name, Concurrency, OnInterrupt string
		Triggers                       []string
	}
	if err := json.Unmarshal([]byte(h.must("events", "--json")), &events); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"backup skip rerun [cron 0 3 * * * manual]",
		"deploy queue abandon [webhook /hooks/deploy manual]",
		"build queue abandon [file " + src + "]",
		"notify parallel abandon [manual]",
	}
	for i, e := range events {
		if got := e.Name + " " + e.Concurrency + " " + e.OnInterrupt + " [" + strings.Join(e.Triggers, " ") + "]"; i >= len(want) || got != want[i] {
			t.Errorf("kickd events: event %d is %q", i, got)
		}
	}
	if len(events) != len(want) {
		t.Errorf("kickd events lists %d events, want %d", len(events), len(want))
	}

	// Only an event with a manual trigger can be fired by hand.
	if _, errOut, code := h.run("event", "build"); code != 2 || !strings.Contains(errOut, `event "build" has no manual trigger`) || !strings.Contains(errOut, `add "- type: manual"`) {
		t.Errorf("kickd event build: exit %d, %s", code, errOut)
	}
	// An event without triggers is an error in the config.
	lonely := filepath.Join(h.dir, "lonely.yaml")
	writeText(t, lonely, "events:\n  - name: lonely\n    command: 'echo hi'\n")
	if _, errOut, code := h.runRaw("check", "-c", lonely); code != 1 || !strings.Contains(errOut, "triggers is required") || !strings.Contains(errOut, "type: manual") {
		t.Errorf("kickd check of an event without triggers: exit %d, %s", code, errOut)
	}
	// kickd init does not replace a config file.
	if _, errOut, code := h.run("init"); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Errorf("kickd init over a config: exit %d, %s", code, errOut)
	}

	// kickd status tells whether the agent runs, and which process it is.
	agentStatus := func() (running bool, pid int) {
		var st struct {
			Running bool
			Agent   struct{ PID int }
		}
		if err := json.Unmarshal([]byte(h.must("status", "--json")), &st); err != nil {
			t.Fatal(err)
		}
		return st.Running, st.Agent.PID
	}
	if running, _ := agentStatus(); running {
		t.Error("kickd status reports an agent before it starts")
	}
	h.start()
	if running, pid := agentStatus(); !running || pid != h.agent.Process.Pid {
		t.Errorf("kickd status reports running %v, process %d, want the agent %d", running, pid, h.agent.Process.Pid)
	}

	// kickd queue shows the runs that wait or run: deploys queue up.
	h.sleep("deploy", 2)
	var ids []int64
	for range 3 {
		ids = append(ids, h.fire("deploy"))
	}
	waitFor(t, "kickd queue shows one deploy running and two waiting", 30*time.Second, func() bool {
		var q []record
		if err := json.Unmarshal([]byte(h.must("queue", "--json")), &q); err != nil {
			t.Fatal(err)
		}
		n := map[string]int{}
		for _, r := range q {
			n[r.Status]++
		}
		return len(q) == 3 && n["running"] == 1 && n["queued"] == 2
	})
	h.waitRuns("deploy", "the deploys end", 60*time.Second, func(rs []record) bool { return len(rs) == 3 && allFinal(rs) })

	// kickd runs filters the history, and kickd show shows one run.
	var done []record
	if err := json.Unmarshal([]byte(h.must("runs", "--event", "deploy", "--status", "succeeded", "--json")), &done); err != nil || len(done) != 3 {
		t.Errorf("kickd runs --status succeeded: %v %v", done, err)
	}
	show := h.must("show", strconv.FormatInt(ids[0], 10))
	for _, want := range []string{"event     deploy", "status    succeeded", "--- output ---", "event=deploy"} {
		if !containsLine(show, want) {
			t.Errorf("kickd show has no line %q:\n%s", want, show)
		}
	}
	h.stop()
	// On Windows, stop kills the agent, which then cannot record its stop.
	if out := h.must("status"); runtime.GOOS != "windows" && !strings.Contains(out, "agent: stopped at") {
		t.Errorf("kickd status after a stop:\n%s", out)
	}

	if out, _, code := h.runRaw("version"); code != 0 || !strings.HasPrefix(out, "kickd ") {
		t.Errorf("kickd version: exit %d, %q", code, out)
	}
	if out, _, code := h.runRaw("licenses"); code != 0 || !strings.Contains(out, "MIT License") {
		t.Errorf("kickd licenses: exit %d, %.200q", code, out)
	}
	if _, err := os.Stat(h.db); err != nil {
		t.Errorf("the database is not where kickd check said: %v", err)
	}
}

// containsLine reports whether text has a line that is want, ignoring the
// end of the line.
func containsLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimRight(line, "\r ") == want {
			return true
		}
	}
	return false
}
