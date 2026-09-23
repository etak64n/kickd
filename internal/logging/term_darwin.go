//go:build darwin

package logging

import (
	"os"

	"golang.org/x/sys/unix"
)

func isTTY(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
}

func enableVT(*os.File) bool { return true }
