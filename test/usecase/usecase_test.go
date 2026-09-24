//go:build usecase

package usecase

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// Each trigger of the README config fires its event, and the command of the
// event runs in its working directory.
func TestUseCaseTriggers(t *testing.T) {
	t.Parallel()
	// A leading seconds field makes the nightly backup run every second.
	h := setup(t, edit{"schedule: '0 3 * * *'", "schedule: '* * * * * *'"})
	h.start()

	// Manual: kickd event --wait waits for the run and reports it.
	var chain []record
	if err := json.Unmarshal([]byte(h.must("event", "notify", "--wait", "--json")), &chain); err != nil || len(chain) != 1 {
		t.Fatalf("kickd event notify --wait printed %v: %v", chain, err)
	}
	if chain[0].Trigger != "manual" {
		t.Errorf("%v: want the trigger manual", chain[0])
	}
	h.checkRun(chain[0], h.app)

	// Webhook: a request without the token is refused and fires nothing.
	if code := h.post("/hooks/deploy", ""); code != http.StatusUnauthorized {
		t.Fatalf("a request without the token got %d, want 401", code)
	}
	if rs := h.runs("deploy"); len(rs) != 0 {
		t.Fatalf("a request without the token fired deploy: %v", rs)
	}
	if code := h.post("/hooks/deploy", token); code != http.StatusAccepted {
		t.Fatalf("a request with the token got %d, want 202", code)
	}
	rs := h.waitRuns("deploy", "a webhook request fires deploy", 60*time.Second, func(rs []record) bool {
		return len(rs) == 1 && final(rs[0])
	})
	if rs[0].Trigger != "webhook" {
		t.Errorf("%v: want the trigger webhook", rs[0])
	}
	h.checkRun(rs[0], h.app)

	// File changes: a new file in app/src builds the app after the debounce.
	src := filepath.Join(h.app, "src", "main.c")
	if err := os.WriteFile(src, []byte("int main(void) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rs = h.waitRuns("build", "a new file fires build", 60*time.Second, func(rs []record) bool {
		return len(rs) > 0 && final(rs[0])
	})
	if rep := h.checkRun(rs[0], h.app); rs[0].Trigger != "file" || !samePath(rep.file, src) {
		t.Errorf("%v: got the file %q, want %s", rs[0], rep.file, src)
	}

	// Cron: the backup runs on its schedule, in the home directory.
	rs = h.waitRuns("backup", "the schedule fires backup", 60*time.Second, func(rs []record) bool {
		return len(rs) > 0 && final(rs[0])
	})
	if rs[0].Trigger != "cron" {
		t.Errorf("%v: want the trigger cron", rs[0])
	}
	h.checkRun(rs[0], h.backup)
	h.stop()
}

// The concurrency settings of the README config: deploys wait for each
// other, a firing while the backup runs is skipped, and notifications do
// not wait for each other.
func TestUseCaseConcurrency(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.sleep("deploy", 1)
	h.sleep("backup", 3)
	h.sleep("notify", 3)
	h.start()

	// deploy has concurrency: queue.
	for range 3 {
		h.fire("deploy")
	}
	rs := h.waitRuns("deploy", "three deploys run", 90*time.Second, func(rs []record) bool {
		return len(rs) == 3 && allFinal(rs)
	})
	for i, r := range rs {
		h.checkRun(r, h.app)
		if i > 0 && r.started().Before(rs[i-1].finished()) {
			t.Errorf("%v started before %v finished", r, rs[i-1])
		}
	}

	// backup has concurrency: skip.
	first := h.fire("backup")
	h.waitRuns("backup", "the backup starts", 60*time.Second, func(rs []record) bool {
		return status(rs, first) == "running"
	})
	second := h.fire("backup")
	rs = h.waitRuns("backup", "both backups end", 60*time.Second, func(rs []record) bool {
		return len(rs) == 2 && allFinal(rs)
	})
	r1, _ := find(rs, first)
	r2, _ := find(rs, second)
	h.checkRun(r1, h.backup)
	if r2.Status != "skipped" || r2.Reason != "already_running" || r1.Skipped != 1 {
		t.Errorf("a backup fired while one runs: %v, and the running one counts %d skipped", r2, r1.Skipped)
	}

	// notify has concurrency: parallel.
	a, b := h.fire("notify"), h.fire("notify")
	rs = h.waitRuns("notify", "both notifications end", 60*time.Second, func(rs []record) bool {
		return len(rs) == 2 && allFinal(rs)
	})
	ra, _ := find(rs, a)
	rb, _ := find(rs, b)
	h.checkRun(ra, h.app)
	h.checkRun(rb, h.app)
	if !rb.started().Before(ra.finished()) {
		t.Errorf("%v waited for %v", rb, ra)
	}
	h.stop()
}

// The on_interrupt settings of the README config, when a crash or a stop of
// kickd cuts runs off: the backup runs again when kickd starts, and the
// deploy does not.
func TestUseCaseInterrupt(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"crash", "stop"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			if how == "stop" && runtime.GOOS == "windows" {
				t.Skip("Windows has no signal that asks a console program to stop")
			}
			h := setup(t)
			h.sleep("backup", 4)
			h.sleep("deploy", 4)
			h.start()
			backup, deploy := h.fire("backup"), h.fire("deploy")
			for event, id := range map[string]int64{"backup": backup, "deploy": deploy} {
				h.waitRuns(event, event+" starts", 60*time.Second, func(rs []record) bool {
					return status(rs, id) == "running"
				})
			}
			cut := time.Now()
			if how == "crash" {
				h.kill()
			} else {
				h.stop()
			}
			h.start()

			reason := map[string]string{"crash": "agent_crashed", "stop": "agent_stopped"}[how]
			rs := h.waitRuns("backup", "the backup runs again", 90*time.Second, func(rs []record) bool {
				return len(rs) == 2 && final(rs[1])
			})
			if r, _ := find(rs, backup); r.Status != "retried" || r.Reason != reason {
				t.Errorf("the backup that was cut off: %v, want retried %s", r, reason)
			}
			if r := rs[1]; r.RetryOf != backup || r.Attempt != 2 {
				t.Errorf("the backup that runs again: %v, retry of %d", r, r.RetryOf)
			}
			h.checkRun(rs[1], h.backup)

			rs = h.waitRuns("deploy", "the deploy is given up", 60*time.Second, func(rs []record) bool {
				return final(rs[0])
			})
			if len(rs) != 1 || rs[0].Status != "abandoned" || rs[0].Reason != reason {
				t.Errorf("the deploy that was cut off: %v, want one run abandoned %s", rs, reason)
			}
			h.stop()
			// A crash leaves the scripts that it cut off running; let them end
			// before the home directory is removed.
			time.Sleep(time.Until(cut.Add(8 * time.Second)))
		})
	}
}

// A command that runs past its timeout is stopped, and its run fails.
func TestUseCaseTimeout(t *testing.T) {
	t.Parallel()
	h := setup(t, edit{"timeout: 1m", "timeout: 2s"})
	h.sleep("notify", 60)
	h.start()
	out, errOut, code := h.run("event", "notify", "--wait", "--json")
	var chain []record
	if err := json.Unmarshal([]byte(out), &chain); err != nil || len(chain) != 1 {
		t.Fatalf("kickd event notify --wait printed %q %q: %v", out, errOut, err)
	}
	r := chain[0]
	if code != 1 || r.Status != "failed" || r.Reason != "timeout" {
		t.Errorf("exit %d, %v: want exit 1 and a run failed by the timeout", code, r)
	}
	if r.DurationMs == nil || *r.DurationMs > 20000 {
		t.Errorf("%v: the command was stopped after %v ms", r, r.DurationMs)
	}
	h.stop()
}

// Saving the config applies it without a restart.
func TestUseCaseReload(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.start()
	b, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// A new event that runs the script of notify.
	added := "\n  - name: hello\n    command: ['./notify.sh']\n    workdir: '~/app'\n"
	if runtime.GOOS == "windows" {
		added = "\n  - name: hello\n    command: ['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'notify.ps1']\n    workdir: '" + h.app + "'\n"
	}
	offset := fileSize(h.log)
	h.write(string(b) + added)
	h.waitLog(offset, "Config reloaded", 30*time.Second)
	var chain []record
	if err := json.Unmarshal([]byte(h.must("event", "hello", "--wait", "--json")), &chain); err != nil || len(chain) != 1 {
		t.Fatalf("kickd event hello --wait printed %v: %v", chain, err)
	}
	h.checkRun(chain[0], h.app)
	h.stop()
}

// kickd cancel stops a command while it runs.
func TestUseCaseCancel(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.sleep("backup", 60)
	h.start()
	id := h.fire("backup")
	h.waitRuns("backup", "the backup starts", 60*time.Second, func(rs []record) bool {
		return status(rs, id) == "running"
	})
	h.must("cancel", strconv.FormatInt(id, 10))
	rs := h.waitRuns("backup", "the backup is canceled", 30*time.Second, func(rs []record) bool {
		return final(rs[0])
	})
	if r := rs[0]; r.Status != "canceled" || r.DurationMs == nil || *r.DurationMs > 20000 {
		t.Errorf("%v: want canceled well before the command ends", r)
	}
	h.stop()
}
