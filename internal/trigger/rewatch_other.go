//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd)

package trigger

import "time"

// rewatchNewDirs is false where fsnotify does not watch with kqueue: with
// inotify and on Windows, the watch that the file watcher adds keeps its
// flags.
const rewatchNewDirs = false

// rewatchDelay is unused where rewatchNewDirs is false.
const rewatchDelay = time.Duration(0)
