//go:build !windows

package config

import "os"

// System reports whether kickd runs for the whole machine: as root, which
// sudo, a LaunchDaemon and a system-wide unit of systemd run it as.
func System() bool { return os.Geteuid() == 0 }
