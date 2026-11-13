//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"os/user"
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

// The account of the user who runs the test has the ids of that user.
func TestLookupAccountFindsTheUserAndItsGroups(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	a, err := lookupAccount(u.Username)
	if err != nil {
		t.Fatal(err)
	}
	if int(a.uid) != os.Getuid() || int(a.gid) != os.Getgid() || a.name != u.Username || a.home != u.HomeDir {
		t.Fatalf("account = %+v, want uid %d, gid %d, home %s", a, os.Getuid(), os.Getgid(), u.HomeDir)
	}
	if len(a.groups) == 0 {
		t.Fatalf("account %s has no groups", a.name)
	}
}

// The payload file of a run with a user is in the shared temporary
// directory, belongs to that user and stays closed to others.
func TestPayloadFileOfAnAccountBelongsToIt(t *testing.T) {
	acct := &account{name: "self", accountIDs: accountIDs{uid: uint32(os.Getuid()), gid: uint32(os.Getgid())}}
	path, err := writeEventFile([]byte(`{}`), acct)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if filepath.Dir(path) != accountTempDir {
		t.Fatalf("payload file %s is not in %s", path, accountTempDir)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	if int(sys.Uid) != os.Getuid() || st.Mode().Perm() != 0o600 {
		t.Fatalf("payload file: uid %d, mode %v", sys.Uid, st.Mode().Perm())
	}
}

// As root, the command and the stop command run as the user of the event.
func TestCommandRunsAsTheUserOfItsEvent(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root can start a command as another user")
	}
	if _, err := user.Lookup("nobody"); err != nil {
		t.Skip("no user nobody:", err)
	}
	r, _, _ := newRunner(t)
	// The temporary directory of root can be closed to nobody, so the
	// command works in the root directory.
	job := Spec{Name: "as-nobody", Shell: `id -un; test -r "$KICKD_PAYLOAD_FILE" && echo payload`, Workdir: "/", User: "nobody"}
	res := r.Execute(context.Background(), job, fileEvent())
	if want := "nobody\npayload\n"; res.ExitCode != 0 || res.Output != want {
		t.Fatalf("result = %+v, want output %q", res, want)
	}
}
