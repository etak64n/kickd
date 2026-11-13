//go:build e2e && windows

package windows

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSavingTheConfigAddsAnEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	h.save("edits/config.edited.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	if r := h.waitForRun(h.fire("test")); r.Status != "succeeded" || strings.TrimSpace(r.Output) != "tested" {
		t.Errorf("test: %s\n%s", r.Status, r.Output)
	}
}

func TestSavingABrokenConfigKeepsThePreviousConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	log := h.path("kickd.log")
	h.waitForLog(log, 0, "File watch started", 1)
	offset := fileSize(log)
	h.save("edits/config.broken.yaml")
	h.waitForLog(log, offset, "Config reload failed", 1)
	// The commands of kickd read the config too, and fail on the broken
	// one, so the log shows that build still runs.
	h.write("src/main.c", "int main(void) { return 1; }\n")
	h.waitForLog(log, offset, "Run completed", 1)
}

func TestARunWaitingInTheQueueRunsWithTheSavedConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.start()
	first := h.fire("deploy")
	h.waitFor("the first deploy runs", 30*time.Second, func() bool { return h.show(first).Status == "running" })
	second := h.fire("deploy")
	offset := fileSize(h.path("kickd.log"))
	h.save("edits/config.production.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	// The release is ready, so the first deploy ends and the second starts.
	h.write("ready", "")
	if r := h.waitForRun(second); r.Status != "succeeded" || r.line("target") != "production" {
		t.Errorf("the deploy that waited: %s %s\n%s", r.Status, r.Reason, r.Output)
	}
}

func TestChangingTheDatabasePathKeepsTheOldDatabase(t *testing.T) {
	t.Parallel()
	h := newHome(t, "reload")
	h.startWatching(1)
	log := h.path("kickd.log")
	saved, err := os.ReadFile(h.config())
	if err != nil {
		t.Fatal(err)
	}
	offset := fileSize(log)
	h.save("edits/config.database.yaml")
	h.waitForLog(log, offset, "Database path change needs a restart", 1)
	h.waitForLog(log, offset, "File watch started", 1)
	h.write("src/main.c", "int main(void) { return 1; }\n")
	h.waitForLog(log, offset, "Run completed", 1)
	h.kill()
	// The commands of kickd read the database that the config names, so
	// the config names the database of the agent again.
	h.write("config.yaml", string(saved))
	if rs := h.runs("build"); len(rs) != 1 || rs[0].Trigger != "file" {
		t.Errorf("the runs of build in kickd.db: %+v", rs)
	}
	if _, err := os.Stat(h.path("runs.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("runs.db: %v, want no such file", err)
	}
}
