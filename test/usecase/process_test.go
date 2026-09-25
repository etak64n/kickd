//go:build usecase

package usecase

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The scripts of the process tests start a process that sleeps and write
// its process ID to EVENT.pid in the home directory. spawn-quiet leaves it
// running with its output redirected, spawn-holding leaves it running with
// the output of the command open, and tree waits for it.
var processScripts = map[string]string{
	"spawn-quiet.sh": `#!/bin/sh
sleep 30 >/dev/null 2>&1 &
echo $! > "$HOME/$KICKD_EVENT.pid"
echo started
`,
	"spawn-holding.sh": `#!/bin/sh
sleep 30 &
echo $! > "$HOME/$KICKD_EVENT.pid"
echo started
`,
	"tree.sh": `#!/bin/sh
sh -c 'echo $$ > "$HOME/$KICKD_EVENT.pid"; exec sleep 60' &
wait
`,
	// Start-Process without -NoNewWindow gives the process a console of its
	// own; with it, the process shares the console and the output handles.
	// The processes run in the temporary folder, so that the test can
	// remove its own folders while they run.
	"spawn-quiet.ps1": `$p = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 30' -WindowStyle Hidden -WorkingDirectory $env:TEMP -PassThru
Set-Content -LiteralPath (Join-Path $env:USERPROFILE "$env:KICKD_EVENT.pid") -Value $p.Id
Write-Output 'started'
`,
	"spawn-holding.ps1": `$p = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 30' -NoNewWindow -WorkingDirectory $env:TEMP -PassThru
Set-Content -LiteralPath (Join-Path $env:USERPROFILE "$env:KICKD_EVENT.pid") -Value $p.Id
Write-Output 'started'
`,
	"tree.ps1": `$p = Start-Process -FilePath powershell -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 60' -WindowStyle Hidden -PassThru
Set-Content -LiteralPath (Join-Path $env:USERPROFILE "$env:KICKD_EVENT.pid") -Value $p.Id
$p.WaitForExit()
`,
}

// setupProcesses adds the events of the process tests to the README config.
func setupProcesses(t *testing.T) *home {
	t.Helper()
	h := setup(t)
	ext := ".sh"
	if runtime.GOOS == "windows" {
		ext = ".ps1"
	}
	var added strings.Builder
	for _, e := range []struct{ name, script, extra string }{
		{"spawn-quiet", "spawn-quiet", ""},
		{"spawn-holding", "spawn-holding", ""},
		{"tree-timeout", "tree", "    timeout: 2s\n"},
		{"tree-cancel", "tree", ""},
	} {
		file := e.script + ext
		if err := os.WriteFile(filepath.Join(h.app, file), []byte(processScripts[file]), 0o755); err != nil {
			t.Fatal(err)
		}
		command := "['./" + file + "']"
		if runtime.GOOS == "windows" {
			command = "['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', '" + file + "']"
		}
		added.WriteString("\n  - name: " + e.name + "\n    command: " + command + "\n    workdir: '" + h.app + "'\n" + e.extra +
			"    triggers:\n      - type: manual\n")
	}
	b, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.write(string(b) + added.String())
	h.must("check")
	return h
}

// pid waits for the process ID that the script of event wrote.
func (h *home) pid(event string) int {
	h.t.Helper()
	var pid int
	waitFor(h.t, event+" writes the process ID", 30*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(h.dir, event+".pid"))
		pid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(b), "\uFEFF")))
		return err == nil && pid > 0
	})
	return pid
}

// endPID ends the process pid and waits until it is gone. On Windows, the
// working directory of a process cannot be removed while it runs.
func endPID(t *testing.T, pid int) {
	killPID(pid)
	waitFor(t, "the process "+strconv.Itoa(pid)+" ends", 30*time.Second, func() bool { return !alive(pid) })
}

// A command that leaves a process running in the background: the run ends
// when the command exits, and kickd does not stop the process. When the
// process keeps the output of the command open, kickd waits 10 seconds for
// it and records the run as failed with the reason wait_failed.
func TestUseCaseBackgroundProcesses(t *testing.T) {
	t.Parallel()
	h := setupProcesses(t)
	h.start()

	id := h.fire("spawn-quiet")
	r := h.waitRun(id, 60*time.Second)
	pid := h.pid("spawn-quiet")
	t.Cleanup(func() { endPID(t, pid) })
	if r.Status != "succeeded" || r.DurationMs == nil || *r.DurationMs > 8000 {
		t.Errorf("a command that leaves a quiet process: %v, %v ms\n%s", r, r.DurationMs, r.Output)
	}
	if !alive(pid) {
		t.Errorf("the quiet process %d does not run after the command", pid)
	}

	id = h.fire("spawn-holding")
	r = h.waitRun(id, 60*time.Second)
	pid = h.pid("spawn-holding")
	t.Cleanup(func() { endPID(t, pid) })
	if r.Status != "failed" || r.Reason != "wait_failed" || r.DurationMs == nil || *r.DurationMs < 9000 || *r.DurationMs > 25000 {
		t.Errorf("a command that leaves a process holding its output: %v, %v ms\n%s", r, r.DurationMs, r.Output)
	}
	if !alive(pid) {
		t.Errorf("the process %d that holds the output does not run after the command", pid)
	}
	h.stop()
}

// A timeout and kickd cancel stop the command and the processes that it
// started.
func TestUseCaseProcessTree(t *testing.T) {
	t.Parallel()
	h := setupProcesses(t)
	h.start()

	id := h.fire("tree-timeout")
	pid := h.pid("tree-timeout")
	t.Cleanup(func() { endPID(t, pid) })
	if r := h.waitRun(id, 60*time.Second); r.Status != "failed" || r.Reason != "timeout" {
		t.Errorf("tree-timeout: %v", r)
	}
	waitFor(t, "the timeout stops the child of the command", 20*time.Second, func() bool { return !alive(pid) })

	id = h.fire("tree-cancel")
	pid = h.pid("tree-cancel")
	t.Cleanup(func() { endPID(t, pid) })
	h.must("cancel", strconv.FormatInt(id, 10))
	if r := h.waitRun(id, 60*time.Second); r.Status != "canceled" {
		t.Errorf("tree-cancel: %v", r)
	}
	waitFor(t, "kickd cancel stops the child of the command", 20*time.Second, func() bool { return !alive(pid) })
	h.stop()
}
