//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone reports whether the process no longer runs. A zombie that waits
// for its new parent to reap it counts as gone.
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err == nil {
		fields := strings.Fields(string(stat))
		return len(fields) > 2 && fields[2] == "Z"
	}
	return false
}

func TestStopKillsChildrenInProcessGroup(t *testing.T) {
	shortGrace(t)
	r, _, _ := newRunner(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	job := helperJob("tree", "KICKD_HELPER_PIDFILE", pidFile)
	job.Timeout = 500 * time.Millisecond
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Reason != "timeout" {
		t.Fatalf("result = %+v", res)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	deadline := time.Now().Add(5 * time.Second)
	for !gone(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d that ignores SIGTERM survived its stopped parent", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestShutdownRecordsCleanExitAsShutdown(t *testing.T) {
	r, rec, cancel := newRunner(t)
	done := make(chan struct{})
	var res struct {
		exit   int
		reason string
	}
	go func() {
		out := r.Execute(context.Background(), helperJob("trap-term"), fileEvent())
		res.exit, res.reason = out.ExitCode, out.Reason
		close(done)
	}()
	rec.waitFor(t, "Run output", 1, 10*time.Second) // the child has installed its handler
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
	if res.exit != 0 || res.reason != "shutdown" {
		t.Fatalf("an exit with code 0 during shutdown must still count as shutdown: exit=%d reason=%q", res.exit, res.reason)
	}
}

func TestStopCommandRunsBeforeSIGTERM(t *testing.T) {
	r, rec, _ := newRunner(t)
	stopFile := filepath.Join(t.TempDir(), "stop")
	job := helperJob("wait-stopfile", "KICKD_HELPER_STOPFILE", stopFile)
	job.StopShell = `printf %s "$KICKD_STOP_REASON" > "$KICKD_HELPER_STOPFILE"`
	job.StopTimeout = 5 * time.Second
	job.Timeout = 300 * time.Millisecond
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Reason != "timeout" {
		t.Fatalf("result = %+v", res)
	}
	// The command ignores SIGTERM, so it ends only through the stop command.
	if !strings.Contains(res.Output, "STOP=timeout") {
		t.Fatalf("the command did not see the stop command, output %q", res.Output)
	}
	if res.Signal != "" {
		t.Fatalf("the command was killed by %s instead of stopping", res.Signal)
	}
	if got := rec.find("Stop command completed"); len(got) != 1 {
		t.Fatalf("Stop command completed logged %d times", len(got))
	}
}

func TestStopCommandFailureStillSendsSIGTERM(t *testing.T) {
	r, rec, _ := newRunner(t)
	job := helperJob("trap-term")
	job.StopShell = "echo cannot stop >&2; exit 3"
	job.StopTimeout = 5 * time.Second
	job.Timeout = 300 * time.Millisecond
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Reason != "timeout" || res.ExitCode != 0 {
		t.Fatalf("SIGTERM after a failed stop command must end the command: %+v", res)
	}
	got := rec.find("Stop command failed")
	if len(got) != 1 || got[0]["reason"] != "exit_code" || got[0]["exitCode"] != int64(3) {
		t.Fatalf("Stop command failed = %v", got)
	}
	if tail, _ := got[0]["stderrTail"].(string); !strings.Contains(tail, "cannot stop") {
		t.Fatalf("stderrTail = %q", tail)
	}
}

func TestStopTimeoutBoundsTheWaitBeforeSIGKILL(t *testing.T) {
	r, _, _ := newRunner(t)
	job := helperJob("ignore-term")
	job.StopTimeout = 200 * time.Millisecond
	job.Timeout = 100 * time.Millisecond
	start := time.Now()
	res := r.Execute(context.Background(), job, fileEvent())
	if res.Reason != "timeout" || res.Signal != "SIGKILL" {
		t.Fatalf("result = %+v", res)
	}
	// killGrace (10 seconds) would apply without stop_timeout.
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the run took %v; stop_timeout did not bound the wait", d)
	}
}
