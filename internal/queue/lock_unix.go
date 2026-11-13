//go:build !windows

package queue

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f without waiting. The lock belongs
// to the open file, so it ends when f is closed or the process exits.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}
