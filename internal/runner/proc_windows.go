//go:build windows

package runner

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// shellCommand runs script through cmd.exe. The raw command line is passed
// so that quotes inside the script survive.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /S /C "` + script + `"`}
	return cmd
}

func setSysProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// terminate kills the process tree; Windows has no graceful signal that
// console-less services can deliver reliably.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func killTree(*exec.Cmd) error { return nil }

// exitSignal is empty on Windows, where processes end with exit codes.
func exitSignal(*os.ProcessState) string { return "" }
