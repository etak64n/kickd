//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package trigger

import "time"

// rewatchNewDirs is true where fsnotify watches with kqueue. When a
// directory appears in a watched directory, fsnotify sends its Create event
// and then watches the new directory for its removal and renames only. The
// file watcher watches the directory for every change as soon as it
// receives the event, and when the watch of fsnotify comes second, it
// replaces those flags, so the changes in the new directory go unnoticed.
// The file watcher therefore watches each new directory once more, a
// moment later, when the watch of fsnotify is in place.
const rewatchNewDirs = true

// rewatchDelay is how long after its first watch a new directory is
// watched again.
const rewatchDelay = 200 * time.Millisecond
