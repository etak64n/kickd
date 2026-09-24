// Package licenses holds the license texts that "kickd licenses" prints:
// the license of kickd, and those of the Go standard library and the Go
// modules that the kickd executables include.
package licenses

import _ "embed"

// Text is the content of licenses.txt, which scripts/third-party-licenses.sh
// writes. CI fails when it no longer matches the dependencies.
//
//go:embed licenses.txt
var Text string
