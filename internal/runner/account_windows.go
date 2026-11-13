//go:build windows

package runner

import (
	"errors"
	"os/exec"
)

// account is the user of an event, which Windows does not support: the
// config rejects user there, so the account has only what the code of
// every OS reads.
type account struct {
	name string
	home string
}

// accountTempDir is unused on Windows, where a command cannot run as the user
// of its event.
const accountTempDir = ""

// lookupAccount fails on Windows. The config does not load an event with a
// user there, so a run never asks.
func lookupAccount(name string) (*account, error) {
	return nil, errors.New("user is not supported on Windows")
}

func runAs(cmd *exec.Cmd, a *account) {}

func giveTo(path string, a *account) error { return nil }
