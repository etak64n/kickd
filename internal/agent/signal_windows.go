//go:build windows

package agent

import "context"

// notifyHangup is a no-op: Windows has no SIGHUP. Edit the config file to
// reload it.
func notifyHangup(context.Context, func()) {}
