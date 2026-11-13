package trigger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchAgain reports the files of the new directory as created and
// watches the directory, so the changes in it reach the watcher.
func TestWatchAgainReportsTheFilesOfANewDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lib")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "list.c"))
	w := &FileWatcher{Event: "build", Root: root, Recursive: true}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	got := w.watchAgain(watcher, slog.New(slog.NewTextHandler(io.Discard, nil)), dir, w.opSet())
	if len(got) != 1 || got[0].Path != filepath.Join(dir, "list.c") || got[0].Op != "create" {
		t.Fatalf("changes = %+v", got)
	}
	touch(t, filepath.Join(dir, "tree.c"))
	deadline := time.After(5 * time.Second)
	for {
		select {
		case fe := <-watcher.Events:
			if fe.Name == filepath.Join(dir, "tree.c") {
				return
			}
		case <-deadline:
			t.Fatal("no event for a file created in the directory that watchAgain watches")
		}
	}
}

// A directory that is gone by the time it is watched again is skipped
// without a problem.
func TestWatchAgainSkipsADirectoryThatIsGone(t *testing.T) {
	w := &FileWatcher{Event: "build", Root: t.TempDir(), Recursive: true}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if got := w.watchAgain(watcher, slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(w.Root, "gone"), w.opSet()); len(got) != 0 {
		t.Fatalf("changes = %+v", got)
	}
}
