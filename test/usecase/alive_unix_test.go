//go:build usecase && !windows

package usecase

import (
	"errors"
	"syscall"
)

// alive reports whether the process pid exists.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killPID ends the process pid, if it still exists.
func killPID(pid int) { syscall.Kill(pid, syscall.SIGKILL) }
