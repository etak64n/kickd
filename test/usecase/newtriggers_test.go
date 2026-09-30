//go:build usecase

package usecase

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// echoVars is a command that prints the variables named, as NAME=value.
func echoVars(names ...string) string {
	var parts []string
	for _, n := range names {
		if runtime.GOOS == "windows" {
			parts = append(parts, n+"=%"+n+"%")
		} else {
			parts = append(parts, n+"=$"+n)
		}
	}
	return "'echo " + strings.Join(parts, " ") + "'"
}

// exitCode is a command that exits with the code in the parameter code.
func exitCode() string {
	if runtime.GOOS == "windows" {
		return "'exit %KICKD_DATA_CODE%'"
	}
	return `'exit "$KICKD_DATA_CODE"'`
}

// An after trigger fires its event when a run of another event ends with a
// listed status: a run that fails, one that succeeds, one that kickd
// cancel ends while it waits, and a chain of after triggers.
func TestUseCaseAfter(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.addEvents(`
  - name: check
    command: ` + exitCode() + `
    params:
      - name: code
        default: '3'
    triggers:
      - type: manual
  - name: alert
    command: ` + echoVars("KICKD_AFTER_EVENT", "KICKD_AFTER_RUN_ID", "KICKD_AFTER_STATUS", "KICKD_AFTER_EXIT_CODE") + `
    concurrency: queue
    triggers:
      - type: after
        event: check
        status: [failed, canceled]
  - name: report
    command: ` + noop() + `
    concurrency: queue
    triggers:
      - type: after
        event: alert
        status: [succeeded]
`)
	h.start()

	// A failed run of check fires alert, which gets the run of check, and
	// alert fires report.
	failed := h.fire("check")
	rs := h.waitRuns("alert", "a failed check fires alert", 60*time.Second, func(rs []record) bool { return len(rs) == 1 && final(rs[0]) })
	r := h.show(rs[0].ID)
	rep := parseReport(strings.ReplaceAll(r.Output, " ", "\n"))
	if r.Status != "succeeded" || r.Trigger != "after" || rep["KICKD_AFTER_EVENT"] != "check" || rep["KICKD_AFTER_RUN_ID"] != strconv.FormatInt(failed, 10) ||
		rep["KICKD_AFTER_STATUS"] != "failed" || rep["KICKD_AFTER_EXIT_CODE"] != "3" {
		t.Errorf("alert: %v\n%s", r, r.Output)
	}
	h.waitRuns("report", "alert fires report", 60*time.Second, func(rs []record) bool { return len(rs) == 1 && final(rs[0]) })

	// A run of check that succeeds fires nothing.
	h.fire("check", "code=0")
	h.waitRuns("check", "the second check ends", 60*time.Second, func(rs []record) bool { return len(rs) == 2 && allFinal(rs) })
	time.Sleep(2 * time.Second)
	if rs := h.runs("alert"); len(rs) != 1 {
		t.Errorf("a check that succeeded fired alert: %v", rs)
	}

	// kickd cancel ends a run that waits while the agent is stopped; the
	// agent hands it to alert when it starts again.
	h.stop()
	id := h.fire("check")
	h.must("cancel", strconv.FormatInt(id, 10))
	h.start()
	rs = h.waitRuns("alert", "a canceled check fires alert", 60*time.Second, func(rs []record) bool { return len(rs) == 2 && final(rs[1]) })
	if rep := parseReport(strings.ReplaceAll(h.show(rs[1].ID).Output, " ", "\n")); rep["KICKD_AFTER_STATUS"] != "canceled" || rep["KICKD_AFTER_RUN_ID"] != strconv.FormatInt(id, 10) {
		t.Errorf("alert after the canceled check: %v", rep)
	}
	h.stop()
}

// A startup trigger fires once when the agent starts: not when the config
// is saved again, and again at the next start.
func TestUseCaseStartup(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.addEvents("\n  - name: prepare\n    command: " + noop() + "\n    triggers:\n      - type: startup\n")
	h.start()
	h.waitRuns("prepare", "the start fires prepare", 30*time.Second, func(rs []record) bool { return len(rs) == 1 && final(rs[0]) })
	if r := h.runs("prepare")[0]; r.Trigger != "startup" || r.Status != "succeeded" {
		t.Errorf("prepare: %v", r)
	}
	offset := fileSize(h.log)
	h.addEvents("\n  - name: later\n    command: " + noop() + "\n    triggers:\n      - type: manual\n")
	h.waitLog(offset, "Config reloaded", 30*time.Second)
	time.Sleep(2 * time.Second)
	if rs := h.runs("prepare"); len(rs) != 1 {
		t.Errorf("a reload fired prepare: %v", rs)
	}
	h.stop()
	h.start()
	h.waitRuns("prepare", "the next start fires prepare", 30*time.Second, func(rs []record) bool { return len(rs) == 2 && allFinal(rs) })
	h.stop()
}

// A wake trigger reads the clocks of the OS, and does not fire while the
// machine stays awake. A runner cannot sleep, so the sleep itself is
// tested with a fake clock in internal/trigger.
func TestUseCaseWakeWhileAwake(t *testing.T) {
	t.Parallel()
	h := setup(t)
	h.addEvents("\n  - name: resync\n    command: " + noop() + "\n    triggers:\n      - type: wake\n")
	offset := fileSize(h.log)
	h.start()
	h.waitLog(offset, "Wake watch started", 30*time.Second)
	time.Sleep(5 * time.Second)
	if rs := h.runs("resync"); len(rs) != 0 {
		t.Errorf("wake fired while the machine was awake: %v", rs)
	}
	h.stop()
}
