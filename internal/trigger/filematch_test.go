package trigger

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// The choice of the changes that a file trigger reports, without watching.

func TestRelGivesThePathUnderTheRootWithSlashes(t *testing.T) {
	root := t.TempDir()
	w := &FileWatcher{Root: root}
	if got := w.rel(filepath.Join(root, "src", "lib", "list.c")); got != "src/lib/list.c" {
		t.Errorf("rel = %q, want src/lib/list.c", got)
	}
}

func TestRelIsEmptyForTheRootItself(t *testing.T) {
	root := t.TempDir()
	if got := (&FileWatcher{Root: root}).rel(root); got != "" {
		t.Errorf("rel = %q, want empty", got)
	}
}

func TestRelIsEmptyForAPathOutsideTheRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "src")
	if got := (&FileWatcher{Root: root}).rel(filepath.Join(filepath.Dir(root), "docs", "guide.md")); got != "" {
		t.Errorf("rel = %q, want empty", got)
	}
}

func TestMatchTakesEveryFileWithoutInclude(t *testing.T) {
	w := &FileWatcher{}
	for _, rel := range []string{"main.c", "docs/guide.md", "Makefile"} {
		if !w.match(rel) {
			t.Errorf("match(%s) = false", rel)
		}
	}
}

func TestMatchTakesOnlyTheFilesThatIncludeNames(t *testing.T) {
	w := &FileWatcher{Include: []string{"*.md"}}
	if !w.match("docs/guide.md") || w.match("docs/notes.txt") {
		t.Errorf("match(docs/guide.md) = %v, match(docs/notes.txt) = %v", w.match("docs/guide.md"), w.match("docs/notes.txt"))
	}
}

func TestMatchLeavesOutAnExcludedFileThatIncludeNames(t *testing.T) {
	w := &FileWatcher{Include: []string{"*.md"}, Exclude: []string{"draft*"}}
	if w.match("docs/draft-faq.md") {
		t.Error("match(docs/draft-faq.md) = true")
	}
}

func TestMatchLeavesOutTheFilesInAnExcludedDirectory(t *testing.T) {
	w := &FileWatcher{Exclude: []string{"node_modules"}}
	if w.match("web/node_modules/left-pad/index.js") {
		t.Error("match(web/node_modules/left-pad/index.js) = true")
	}
}

func TestMatchLeavesOutTheRoot(t *testing.T) {
	if (&FileWatcher{}).match("") {
		t.Error(`match("") = true`)
	}
}

func TestOpSetWatchesTheDefaultChangesWithoutChanges(t *testing.T) {
	set := (&FileWatcher{}).opSet()
	for _, op := range DefaultFileOps {
		if !set[op] {
			t.Errorf("%s is not watched", op)
		}
	}
	if set["chmod"] {
		t.Error("chmod is watched")
	}
}

func TestOpSetWatchesOnlyTheChangesGiven(t *testing.T) {
	set := (&FileWatcher{Ops: []string{"create"}}).opSet()
	if !set["create"] || set["write"] || set["remove"] || set["rename"] {
		t.Errorf("opSet = %v, want only create", set)
	}
}

func TestOpNameNamesEachChangeOfFsnotify(t *testing.T) {
	for op, want := range map[fsnotify.Op]string{
		fsnotify.Create: "create", fsnotify.Write: "write", fsnotify.Remove: "remove",
		fsnotify.Rename: "rename", fsnotify.Chmod: "chmod",
	} {
		if got := opName(op); got != want {
			t.Errorf("opName(%v) = %q, want %q", op, got, want)
		}
	}
}

func TestOpNameNamesACreateThatAlsoWroteACreate(t *testing.T) {
	if got := opName(fsnotify.Create | fsnotify.Write); got != "create" {
		t.Errorf("opName(create|write) = %q, want create", got)
	}
}

func TestDefaultFileOpsAreTheFourKindsOfChange(t *testing.T) {
	if !slices.Equal(DefaultFileOps, []string{"create", "write", "remove", "rename"}) {
		t.Errorf("DefaultFileOps = %v", DefaultFileOps)
	}
}
