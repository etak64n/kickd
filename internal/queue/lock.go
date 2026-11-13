package queue

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// errLocked is the error of lockFile when another process holds the lock.
var errLocked = errors.New("locked")

// lockWait is how long LockAgent tries a lock that is taken.
var lockWait = 5 * time.Second

// AgentRunningError is the error of LockAgent when another agent runs on
// the database.
type AgentRunningError struct {
	Database string
	PID      int // 0 when the file of the lock does not name it
}

func (e *AgentRunningError) Error() string {
	who := "another kickd agent"
	if e.PID > 0 {
		who = fmt.Sprintf("another kickd agent (PID %d)", e.PID)
	}
	return fmt.Sprintf("%s already runs on the database %s; Please stop it before you start this one: two agents on one database would run each firing twice", who, e.Database)
}

// LockAgent takes the lock that lets one agent at a time run on the
// database at path, and returns the function that releases it. The lock
// is a lock of the OS on the file path + ".lock", so the OS releases it
// when the agent exits, even when it crashes. Two agents on one database
// would both run every firing, and each would take the running runs of
// the other for runs that a crash cut off.
func LockAgent(path string) (release func(), err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	name := abs + ".lock"
	f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// The OS releases the lock of an agent that crashed when it ends the
	// process, and Windows can take a moment to do so, so a lock that is
	// taken is tried again for a few seconds before it counts as another
	// agent's.
	deadline := time.Now().Add(lockWait)
	for {
		err = lockFile(f)
		if !errors.Is(err, errLocked) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		f.Close()
		if errors.Is(err, errLocked) {
			return nil, &AgentRunningError{Database: abs, PID: lockHolder(name)}
		}
		return nil, fmt.Errorf("lock %s: %w", name, err)
	}
	// The PID tells a second agent which process to stop.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() { f.Close() }, nil
}

// lockHolder returns the PID that the file of the lock names, or 0.
func lockHolder(name string) int {
	b, err := os.ReadFile(name)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}
