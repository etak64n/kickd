package trigger

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
)

// ValidFileOps lists the file operations a file trigger can subscribe to.
var ValidFileOps = []string{"create", "write", "remove", "rename", "chmod"}

// DefaultFileOps are used when a trigger does not list events.
// chmod is left out because it is noisy and rarely useful.
var DefaultFileOps = []string{"create", "write", "remove", "rename"}

// maxPendingChanges bounds the number of changes collected during one
// debounce window.
const maxPendingChanges = 10000

// FileWatcher fires one event per debounce window when files under Root
// change.
type FileWatcher struct {
	Event     string
	Root      string
	Recursive bool
	Include   []string
	Exclude   []string
	Ops       []string
	Debounce  time.Duration
	Logger    *slog.Logger
	// Internal marks the watcher kickd uses for its own config file. Its
	// start line is logged at DEBUG instead of INFO.
	Internal bool
}

// Run watches until ctx is done. It returns an error only when the root
// directory cannot be watched.
func (w *FileWatcher) Run(ctx context.Context, h event.Handler) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("file watcher: %w", err)
	}
	defer watcher.Close()

	log := w.Logger.With("trigger", event.KindFile)
	if w.Event != "" {
		log = log.With("event", w.Event)
	}
	dirs, err := w.addTree(watcher, log, w.Root, nil)
	if err != nil {
		return fmt.Errorf("watch %s: %w", w.Root, err)
	}
	ops := w.opSet()
	lifecycle := slog.LevelInfo
	if w.Internal {
		lifecycle = slog.LevelDebug
	}
	log.Log(ctx, lifecycle, "File watch started", "file", w.Root, "recursive", w.Recursive, "debounceMs", w.Debounce.Milliseconds(), "count", dirs)
	defer log.Debug("File watch stopped", "file", w.Root)

	var (
		pending []event.FileChange
		seen    = map[string]struct{}{}
		dropped int
		timer   *time.Timer
		fire    <-chan time.Time
		errs    = limiter{every: time.Minute}
	)
	flush := func() {
		if dropped > 0 {
			log.Warn("File changes dropped, too many pending", "file", w.Root, "count", dropped, "thresholdCount", maxPendingChanges)
			dropped = 0
		}
		if len(pending) == 0 {
			return
		}
		ev := event.Event{
			RequestID: event.NewID(),
			Name:      w.Event,
			Trigger:   event.KindFile,
			TriggerID: "file:" + w.Root,
			Time:      time.Now(),
			Files:     pending,
		}
		pending = nil
		seen = map[string]struct{}{}
		status := h.Dispatch(ev)
		log.Debug("File changes collected", "requestId", ev.RequestID, "count", len(ev.Files), "dispatch", string(status))
	}

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case fe, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			changes := w.translate(watcher, log, fe, ops)
			if len(changes) == 0 {
				continue
			}
			for _, c := range changes {
				log.Log(ctx, logging.LevelTrace, "File change detected", "file", c.Path, "op", c.Op)
				key := c.Op + "\x00" + c.Path
				if _, dup := seen[key]; dup {
					continue
				}
				if len(pending) >= maxPendingChanges {
					dropped++
					continue
				}
				seen[key] = struct{}{}
				pending = append(pending, c)
			}
			if w.Debounce <= 0 {
				flush()
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(w.Debounce)
			fire = timer.C
		case <-fire:
			timer, fire = nil, nil
			flush()
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			suppressed, allowed := errs.allow()
			if !allowed {
				continue
			}
			attrs := []any{"file", w.Root, logging.Err(err)}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				attrs = append(attrs, "reason", "event_overflow")
			}
			if suppressed > 0 {
				attrs = append(attrs, "suppressed", suppressed)
			}
			log.Warn("File watch error", attrs...)
		}
	}
}

// limiter lets one event through per interval and counts the rest, so
// that a burst of identical errors becomes one line with a count.
type limiter struct {
	every      time.Duration
	last       time.Time
	suppressed int
}

func (l *limiter) allow() (suppressed int, ok bool) {
	now := time.Now()
	if !l.last.IsZero() && now.Sub(l.last) < l.every {
		l.suppressed++
		return 0, false
	}
	n := l.suppressed
	l.suppressed, l.last = 0, now
	return n, true
}

// translate converts an fsnotify event into zero or more file changes,
// registering newly created directories when watching recursively.
func (w *FileWatcher) translate(watcher *fsnotify.Watcher, log *slog.Logger, fe fsnotify.Event, ops map[string]bool) []event.FileChange {
	rel := w.rel(fe.Name)
	if rel == "" {
		return nil
	}
	var out []event.FileChange
	op := opName(fe.Op)
	if ops[op] && w.match(rel) {
		out = append(out, event.FileChange{Path: fe.Name, Op: op})
	}
	if w.Recursive && fe.Has(fsnotify.Create) && !w.excluded(rel) {
		info, err := os.Lstat(fe.Name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Removed again before it could be inspected.
		case err != nil:
			log.Debug("New path not inspected", "file", fe.Name, logging.Err(err))
		case info.IsDir():
			// Files written into the directory before the watch was in
			// place would be missed, so report what is already there.
			_, err := w.addTree(watcher, log, fe.Name, func(p string) {
				if ops["create"] && w.match(w.rel(p)) {
					out = append(out, event.FileChange{Path: p, Op: "create"})
				}
			})
			if err != nil {
				log.Warn("Directory not watched", "file", fe.Name, "reason", "watch_failed", logging.Err(err))
			}
		}
	}
	return out
}

// addTree watches root and, when recursive, every directory below it that
// is not excluded. onFile is called for each regular file found. It
// returns the number of directories watched. Directories that cannot be
// watched are logged once with the first error and a count.
func (w *FileWatcher) addTree(watcher *fsnotify.Watcher, log *slog.Logger, root string, onFile func(string)) (int, error) {
	if !w.Recursive {
		if err := watcher.Add(root); err != nil {
			return 0, err
		}
		return 1, nil
	}
	dirs, failed := 0, 0
	var firstErr error
	var firstPath string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			log.Warn("Path skipped while scanning", "file", p, logging.Err(err))
			return nil
		}
		if d.IsDir() {
			if p != root && w.excluded(w.rel(p)) {
				return fs.SkipDir
			}
			if err := watcher.Add(p); err != nil {
				if p == root {
					return err
				}
				if failed == 0 {
					firstErr, firstPath = err, p
				}
				failed++
				return nil
			}
			dirs++
			return nil
		}
		if onFile != nil {
			onFile(p)
		}
		return nil
	})
	if failed > 0 {
		log.Warn("Directories not watched", "file", firstPath, "count", failed, "reason", "watch_failed", logging.Err(firstErr))
	}
	return dirs, err
}

// rel returns the slash separated path of p relative to Root, or "" when
// p is the root itself or lies outside it.
func (w *FileWatcher) rel(p string) string {
	r, err := filepath.Rel(w.Root, p)
	if err != nil || r == "." {
		return ""
	}
	r = filepath.ToSlash(r)
	if r == ".." || strings.HasPrefix(r, "../") {
		return ""
	}
	return r
}

func (w *FileWatcher) excluded(rel string) bool {
	return matchAny(w.Exclude, rel, true)
}

func (w *FileWatcher) match(rel string) bool {
	if rel == "" || w.excluded(rel) {
		return false
	}
	return len(w.Include) == 0 || matchAny(w.Include, rel, false)
}

func (w *FileWatcher) opSet() map[string]bool {
	ops := w.Ops
	if len(ops) == 0 {
		ops = DefaultFileOps
	}
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set
}

// matchAny reports whether rel matches one of the patterns. A pattern with
// a slash is matched against the whole relative path; a pattern without a
// slash is matched against the base name, or against every path component
// when components is true.
func matchAny(patterns []string, rel string, components bool) bool {
	for _, p := range patterns {
		if strings.Contains(p, "/") {
			if ok, _ := path.Match(strings.TrimPrefix(p, "/"), rel); ok {
				return true
			}
			continue
		}
		if components {
			for _, c := range strings.Split(rel, "/") {
				if ok, _ := path.Match(p, c); ok {
					return true
				}
			}
			continue
		}
		if ok, _ := path.Match(p, path.Base(rel)); ok {
			return true
		}
	}
	return false
}

func opName(op fsnotify.Op) string {
	switch {
	case op.Has(fsnotify.Create):
		return "create"
	case op.Has(fsnotify.Remove):
		return "remove"
	case op.Has(fsnotify.Rename):
		return "rename"
	case op.Has(fsnotify.Write):
		return "write"
	case op.Has(fsnotify.Chmod):
		return "chmod"
	}
	return "unknown"
}
