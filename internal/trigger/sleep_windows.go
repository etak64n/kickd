package trigger

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procQueryInterruptTime         = windows.NewLazySystemDLL("kernelbase.dll").NewProc("QueryInterruptTime")
	procQueryUnbiasedInterruptTime = windows.NewLazySystemDLL("kernel32.dll").NewProc("QueryUnbiasedInterruptTime")
)

// sleptSinceBoot returns how long the machine has slept since it booted:
// the interrupt time counts the time asleep, and the unbiased interrupt
// time does not. Both are in units of 100 nanoseconds.
func sleptSinceBoot() (time.Duration, error) {
	if err := procQueryInterruptTime.Find(); err != nil {
		return 0, err
	}
	var all, awake uint64
	procQueryInterruptTime.Call(uintptr(unsafe.Pointer(&all)))
	if ok, _, err := procQueryUnbiasedInterruptTime.Call(uintptr(unsafe.Pointer(&awake))); ok == 0 {
		return 0, err
	}
	return time.Duration(all-awake) * 100, nil
}
