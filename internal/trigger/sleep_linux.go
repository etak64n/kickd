package trigger

import (
	"time"

	"golang.org/x/sys/unix"
)

// sleptSinceBoot returns how long the machine has slept since it booted:
// CLOCK_BOOTTIME counts the time asleep, and CLOCK_MONOTONIC does not.
func sleptSinceBoot() (time.Duration, error) {
	var boot, mono unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &boot); err != nil {
		return 0, err
	}
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &mono); err != nil {
		return 0, err
	}
	return time.Duration(boot.Nano() - mono.Nano()), nil
}
