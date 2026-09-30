package trigger

import (
	"time"

	"golang.org/x/sys/unix"
)

// sleptSinceBoot returns how long the machine has slept since it booted:
// CLOCK_MONOTONIC_RAW counts the time asleep, and CLOCK_UPTIME_RAW does
// not. Both run at the same raw rate, so time adjustments do not count.
func sleptSinceBoot() (time.Duration, error) {
	var all, awake unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &all); err != nil {
		return 0, err
	}
	if err := unix.ClockGettime(unix.CLOCK_UPTIME_RAW, &awake); err != nil {
		return 0, err
	}
	return time.Duration(all.Nano() - awake.Nano()), nil
}
