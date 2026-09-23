//go:build windows

package logging

import (
	"os"
	"syscall"
)

// SignalName returns the conventional name of a signal, such as SIGTERM.
func SignalName(s os.Signal) string {
	switch s {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case os.Kill:
		return "SIGKILL"
	}
	return s.String()
}
