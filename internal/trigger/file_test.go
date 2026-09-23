package trigger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

type collector struct{ ch chan event.Event }

func newCollector() *collector { return &collector{ch: make(chan event.Event, 16)} }

func (c *collector) Dispatch(ev event.Event) event.DispatchStatus {
	c.ch <- ev
	return event.Queued
}

func (c *collector) RunSync(_ context.Context, ev event.Event) (event.Result, error) {
	c.ch <- ev
	return event.Result{}, nil
}

func (c *collector) wait(t *testing.T, d time.Duration) event.Event {
	t.Helper()
	select {
	case ev := <-c.ch:
		return ev
	case <-time.After(d):
		t.Fatal("no event received")
		return event.Event{}
	}
}

func (c *collector) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case ev := <-c.ch:
		t.Fatalf("unexpected event: %+v", ev.Files)
	case <-time.After(d):
	}
}

func startWatcher(t *testing.T, w *FileWatcher) *collector {
	t.Helper()
	w.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c := newCollector()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx, c) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("watcher returned %v", err)
		}
	})
	// Give the watcher a moment to register on platforms that need it.
	time.Sleep(100 * time.Millisecond)
	return c
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func paths(ev event.Event) map[string]string {
	m := map[string]string{}
	for _, f := range ev.Files {
		m[filepath.Base(f.Path)] = f.Op
	}
	return m
}

func TestFileWatcherDebounceCollectsChanges(t *testing.T) {
	dir := t.TempDir()
	c := startWatcher(t, &FileWatcher{Event: "j", Root: dir, Ops: []string{"create"}, Debounce: 300 * time.Millisecond})

	touch(t, filepath.Join(dir, "a.txt"))
	touch(t, filepath.Join(dir, "b.txt"))
	touch(t, filepath.Join(dir, "c.txt"))

	ev := c.wait(t, 5*time.Second)
	if ev.Name != "j" || ev.Trigger != event.KindFile || ev.TriggerID != "file:"+dir {
		t.Errorf("event header = %+v", ev)
	}
	got := paths(ev)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if got[n] != "create" {
			t.Errorf("%s: op = %q, want create (all: %v)", n, got[n], got)
		}
	}
	c.none(t, 500*time.Millisecond)
}

func TestFileWatcherIncludeExcludeRecursive(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"sub", ".git"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := startWatcher(t, &FileWatcher{
		Event: "j", Root: dir, Recursive: true,
		Include: []string{"*.txt"}, Exclude: []string{".git"},
		Ops: []string{"create"}, Debounce: 200 * time.Millisecond,
	})

	touch(t, filepath.Join(dir, ".git", "ignored.txt"))
	touch(t, filepath.Join(dir, "sub", "note.md"))
	touch(t, filepath.Join(dir, "sub", "note.txt"))
	ev := c.wait(t, 5*time.Second)
	got := paths(ev)
	if len(got) != 1 || got["note.txt"] != "create" {
		t.Fatalf("files = %v, want only note.txt", got)
	}

	// A directory created after the watcher started is watched too.
	if err := os.Mkdir(filepath.Join(dir, "later"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "later", "deep.txt"))
	ev = c.wait(t, 5*time.Second)
	if got := paths(ev); got["deep.txt"] != "create" {
		t.Fatalf("files = %v, want deep.txt", got)
	}
}

func TestFileWatcherNonRecursiveIgnoresSubdirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := startWatcher(t, &FileWatcher{Event: "j", Root: dir, Include: []string{"*.txt"}, Ops: []string{"create"}, Debounce: 100 * time.Millisecond})
	touch(t, filepath.Join(dir, "sub", "inner.txt"))
	c.none(t, 500*time.Millisecond)
	touch(t, filepath.Join(dir, "top.txt"))
	if got := paths(c.wait(t, 5*time.Second)); got["top.txt"] != "create" {
		t.Fatalf("files = %v", got)
	}
}

func TestFileWatcherMissingRoot(t *testing.T) {
	w := &FileWatcher{Event: "j", Root: filepath.Join(t.TempDir(), "nope"), Logger: slog.Default()}
	if err := w.Run(context.Background(), newCollector()); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}

func TestMatchAny(t *testing.T) {
	cases := []struct {
		patterns   []string
		rel        string
		components bool
		want       bool
	}{
		{[]string{"*.txt"}, "a/b/c.txt", false, true},
		{[]string{"*.txt"}, "a/b/c.md", false, false},
		{[]string{"a/*.txt"}, "a/c.txt", false, true},
		{[]string{"a/*.txt"}, "a/b/c.txt", false, false},
		{[]string{".git"}, ".git/HEAD", true, true},
		{[]string{".git"}, "src/.git/HEAD", true, true},
		{[]string{".git"}, "src/.gitignore", true, false},
		{[]string{".git"}, "src/.git/HEAD", false, false},
		{[]string{"node_modules", "*.log"}, "app/debug.log", true, true},
		{nil, "anything", false, false},
	}
	for _, c := range cases {
		if got := matchAny(c.patterns, c.rel, c.components); got != c.want {
			t.Errorf("matchAny(%v, %q, %v) = %v, want %v", c.patterns, c.rel, c.components, got, c.want)
		}
	}
}
