//go:build !darwin && !linux && !windows

package logging

import "os"

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func enableVT(*os.File) bool { return true }
