//go:build !windows

package logging

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// SignalName returns the conventional name of a signal, such as SIGTERM.
func SignalName(s os.Signal) string {
	if sig, ok := s.(syscall.Signal); ok {
		if name := unix.SignalName(sig); name != "" {
			return name
		}
	}
	return s.String()
}
