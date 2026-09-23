//go:build !windows

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/etak64n/kickd/internal/logging"
)

// shellCommand runs script through the POSIX shell.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", script)
}

// setSysProcAttr puts the command in its own process group so that the
// whole tree can be signalled.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks the process group to stop. os/exec kills the leader
// after WaitDelay if it ignores the request.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return nil
}

// killTree removes children that outlived the leader. A group that no
// longer exists is the expected case and is not an error.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
		// EPERM: on macOS the group can be a zombie that is no longer ours.
		return nil
	}
	return err
}

// exitSignal names the signal that ended the process, if any.
func exitSignal(ps *os.ProcessState) string {
	if ps == nil {
		return ""
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return logging.SignalName(ws.Signal())
	}
	return ""
}
