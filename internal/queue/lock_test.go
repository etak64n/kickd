package queue

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One agent at a time holds the lock of a database, and the lock is free
// again once the agent releases it.
func TestASecondAgentCannotTakeTheLockOfTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kickd.db")
	release, err := LockAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = LockAgent(path)
	var running *AgentRunningError
	if !errors.As(err, &running) {
		t.Fatalf("second LockAgent = %v, want AgentRunningError", err)
	}
	abs, _ := filepath.Abs(path)
	if running.PID != os.Getpid() || running.Database != abs {
		t.Errorf("error = %+v", running)
	}
	if !strings.Contains(err.Error(), "Please stop it before you start this one") {
		t.Errorf("message = %q", err)
	}
	release()
	again, err := LockAgent(path)
	if err != nil {
		t.Fatalf("LockAgent after release = %v", err)
	}
	again()
}

// The file of the lock is readable by its owner alone, as the database is.
func TestTheLockFileIsPrivate(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows has no permission bits")
	}
	path := filepath.Join(t.TempDir(), "kickd.db")
	release, err := LockAgent(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	st, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
}
