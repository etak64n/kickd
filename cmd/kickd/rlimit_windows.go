//go:build windows

package main

func openFileLimit() (uint64, bool) { return 0, false }
