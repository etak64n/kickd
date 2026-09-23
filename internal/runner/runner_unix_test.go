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
