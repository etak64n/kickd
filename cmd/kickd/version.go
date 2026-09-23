package main

import "runtime/debug"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// resolveVersion returns the version set at build time. Without one, it
// returns the main module version that the go command records: the tag
// for "go install ...@v1.2.3", or a pseudo-version for a build inside a
// git checkout.
func resolveVersion(set string, info *debug.BuildInfo, ok bool) string {
	if set != "dev" || !ok || info == nil {
		return set
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return set
}
