//go:build !linux && !darwin && !windows

package trigger

import (
	"errors"
	"time"
)

// sleptSinceBoot is not available on this OS, so wake triggers fail.
func sleptSinceBoot() (time.Duration, error) {
	return 0, errors.New("wake triggers are not supported on this OS")
}
