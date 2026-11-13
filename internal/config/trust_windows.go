//go:build windows

package config

// checkRootOnly does not check the files of the config on Windows, where
// access control lists, not an owner and permission bits, decide who can
// change a file.
func checkRootOnly(paths []string) error { return nil }
