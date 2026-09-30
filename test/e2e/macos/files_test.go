//go:build e2e && darwin

package macos

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"
)

// watching returns a home with the config of testdata/files, whose agent
// watches src, docs, uploads and conf.
func watching(t *testing.T) *home {
	t.Helper()
	h := newHome(t, "files")
	h.start()
	h.waitForLog(h.path("kickd.log"), 0, "File watch started", 4)
	return h
}

// changes returns the changes in the payloads of the runs, as "op path".
func changes(rs []run) []string {
	var out []string
	for _, r := range rs {
		files, _ := r.Payload["files"].([]any)
		for _, f := range files {
			c, _ := f.(map[string]any)
			out = append(out, fmt.Sprintf("%v %v", c["op"], c["path"]))
		}
	}
	return out
}

// waitForChange waits until the runs of event hold the change "op path".
func (h *home) waitForChange(event, op, path string) {
	h.t.Helper()
	want := op + " " + path
	h.waitFor(fmt.Sprintf("%s gets %q", event, want), 30*time.Second, func() bool {
		rs := h.runs(event)
		for i := range rs {
			rs[i] = h.show(rs[i].ID)
		}
		return slices.Contains(changes(rs), want)
	})
}

// changesAfterAWhile waits 2 seconds, longer than the debounce of every
// event, and returns the changes that the runs of event got.
func (h *home) changesAfterAWhile(event string) []string {
	h.t.Helper()
	time.Sleep(2 * time.Second)
	return changes(h.waitForRuns(event, 1))
}

func TestFileCreatingAFileFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	h.write("src/util.c", "int twice(int n) { return 2 * n; }\n")
	h.waitForChange("build", "create", h.path("src", "util.c"))
}

func TestFileWritingAFileFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	f, err := os.OpenFile(h.path("src", "main.c"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("/* one more line */\n")
	f.Close()
	h.waitForChange("build", "write", h.path("src", "main.c"))
}

func TestFileRenamingAFileFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	if err := os.Rename(h.path("src", "main.c"), h.path("src", "app.c")); err != nil {
		t.Fatal(err)
	}
	h.waitForChange("build", "rename", h.path("src", "main.c"))
}

func TestFileRemovingAFileFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	if err := os.Remove(h.path("src", "main.c")); err != nil {
		t.Fatal(err)
	}
	h.waitForChange("build", "remove", h.path("src", "main.c"))
}

func TestFileCreatingADirectoryFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	if err := os.Mkdir(h.path("src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.waitForChange("build", "create", h.path("src", "lib"))
}

func TestFileAFileInANewDirectoryFiresTheEvent(t *testing.T) {
	t.Parallel()
	h := watching(t)
	if err := os.Mkdir(h.path("src", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.waitForChange("build", "create", h.path("src", "lib"))
	h.write("src/lib/list.c", "struct list;\n")
	h.waitForChange("build", "create", h.path("src", "lib", "list.c"))
}

func TestFileIncludeChoosesTheFiles(t *testing.T) {
	t.Parallel()
	h := watching(t)
	h.write("docs/notes.txt", "notes\n")
	h.write("docs/guide.md", "# Guide\n")
	h.waitForChange("publish", "create", h.path("docs", "guide.md"))
	for _, c := range h.changesAfterAWhile("publish") {
		if c == "create "+h.path("docs", "notes.txt") || c == "write "+h.path("docs", "notes.txt") {
			t.Errorf("publish fired for %s", c)
		}
	}
}

func TestFileExcludeLeavesOutFiles(t *testing.T) {
	t.Parallel()
	h := watching(t)
	h.write("docs/draft-faq.md", "# FAQ\n")
	h.write("docs/guide.md", "# Guide\n")
	h.waitForChange("publish", "create", h.path("docs", "guide.md"))
	for _, c := range h.changesAfterAWhile("publish") {
		if c == "create "+h.path("docs", "draft-faq.md") || c == "write "+h.path("docs", "draft-faq.md") {
			t.Errorf("publish fired for %s", c)
		}
	}
}

func TestFileChangesChooseTheKindsOfChange(t *testing.T) {
	t.Parallel()
	h := watching(t)
	h.write("uploads/2026-09-02.csv", "id,name\n")
	h.waitForChange("import", "create", h.path("uploads", "2026-09-02.csv"))
	h.write("uploads/2026-09-02.csv", "id,name\n2,second\n")
	if err := os.Remove(h.path("uploads", "2026-09-02.csv")); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.changesAfterAWhile("import") {
		if c != "create "+h.path("uploads", "2026-09-02.csv") {
			t.Errorf("import fired for %s", c)
		}
	}
}

func TestFileWithoutRecursiveASubdirectoryDoesNotFire(t *testing.T) {
	t.Parallel()
	h := watching(t)
	h.write("conf/sites/blog.conf", "root = /srv/blog\n")
	h.write("conf/app.conf", "port = 8081\n")
	h.waitForChange("reload", "write", h.path("conf", "app.conf"))
	for _, c := range h.changesAfterAWhile("reload") {
		if c == "create "+h.path("conf", "sites", "blog.conf") || c == "write "+h.path("conf", "sites", "blog.conf") {
			t.Errorf("reload fired for %s", c)
		}
	}
}

func TestFileChangesWithinTheDebounceFireOneRun(t *testing.T) {
	t.Parallel()
	h := watching(t)
	names := []string{"a.c", "b.c", "c.c", "d.c", "e.c"}
	for _, name := range names {
		h.write("src/"+name, "int x;\n")
	}
	h.waitForChange("build", "create", h.path("src", "e.c"))
	got := h.changesAfterAWhile("build")
	if rs := h.runs("build"); len(rs) != 1 {
		t.Fatalf("build ran %d times, want once", len(rs))
	}
	for _, name := range names {
		if !slices.Contains(got, "create "+h.path("src", name)) {
			t.Errorf("the run has no create of %s: %v", name, got)
		}
	}
}
