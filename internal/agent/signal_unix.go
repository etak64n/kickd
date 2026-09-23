//go:build !windows

package agent

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// notifyHangup calls fn on SIGHUP until ctx is done.
func notifyHangup(ctx context.Context, fn func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				fn()
			}
		}
	}()
}
