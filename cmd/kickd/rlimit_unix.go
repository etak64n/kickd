//go:build !windows

package main

import "syscall"

// openFileLimit returns the soft limit on open files. The Go runtime
// raises it to the hard limit at startup, which the kqueue backend of the
// macOS file watcher needs: it holds one descriptor per watched file.
func openFileLimit() (uint64, bool) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0, false
	}
	return uint64(lim.Cur), true
}
