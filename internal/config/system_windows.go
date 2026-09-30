//go:build windows

package config

import "golang.org/x/sys/windows"

// System reports whether kickd runs for the whole machine: as an
// administrator, in a PowerShell opened as administrator or as the kickd
// service, which runs as SYSTEM.
func System() bool {
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	// In the token of an administrator whose PowerShell was not opened as
	// administrator, the group only denies access, and IsMember counts
	// only the groups that grant it.
	member, err := windows.Token(0).IsMember(admins)
	return err == nil && member
}
