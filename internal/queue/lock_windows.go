package queue

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile locks one byte of f, at 4 GiB, without waiting. Windows ends
// the lock when f is closed or the process exits. The byte lies past the
// PID that the file holds, so other processes can still read the PID.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{OffsetHigh: 1}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}
