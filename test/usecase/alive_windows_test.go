//go:build usecase && windows

package usecase

import (
	"os"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code that Windows reports for a running process.
const stillActive = 259

// alive reports whether the process pid still runs.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// killPID ends the process pid, if it still exists.
func killPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}
