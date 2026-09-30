//go:build e2e && darwin

package macos

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The config of testdata/event-files is config.yaml, and deploy.yaml and
// notify.yml next to it add their events.

func TestAnEventOfAnotherYAMLFileRuns(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	h.start()
	if r := h.waitForRun(h.fire("deploy")); r.Status != "succeeded" || strings.TrimSpace(r.Output) != "deployed" {
		t.Errorf("deploy: %s\n%s", r.Status, r.Output)
	}
}

func TestAnEventOfAYmlFileRuns(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	h.start()
	if r := h.waitForRun(h.fire("notify")); r.Status != "succeeded" || r.line("trigger") != "manual" {
		t.Errorf("notify: %s\n%s", r.Status, r.Output)
	}
}

func TestAfterFollowsAnEventOfAnotherFile(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	h.start()
	h.fire("deploy")
	if r := h.waitForRuns("notify", 1)[0]; r.line("trigger") != "after" || r.line("after") != "deploy" {
		t.Errorf("notify:\n%s", r.Output)
	}
}

func TestEventsListsTheFileOfEachEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	var events []struct{ Name, File string }
	if err := json.Unmarshal([]byte(h.must("events", "--json")), &events); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"backup": h.path("config.yaml"), "deploy": h.path("deploy.yaml"), "notify": h.path("notify.yml")}
	for _, e := range events {
		if e.File != want[e.Name] {
			t.Errorf("%s is in %s, want %s", e.Name, e.File, want[e.Name])
		}
	}
	if len(events) != len(want) {
		t.Errorf("kickd events lists %d events, want %d", len(events), len(want))
	}
}

func TestCheckListsTheFilesWhoseEventsItReads(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	out := h.must("check")
	for _, want := range []string{
		"events: " + h.path("config.yaml") + " (1 event)",
		"events: " + h.path("deploy.yaml") + " (1 event)",
		"events: " + h.path("notify.yml") + " (1 event)",
	} {
		if !hasLine(out, want) {
			t.Errorf("kickd check has no line %q:\n%s", want, out)
		}
	}
}

func TestCheckNamesAYAMLFileWithoutEvents(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	if out := h.must("check"); !hasLine(out, "not read: "+h.path("notes.yaml")+" (no events section)") {
		t.Errorf("kickd check:\n%s", out)
	}
}

func TestSavingAFileWithEventsAddsItsEvents(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	h.place("edits/restore.yaml", "restore.yaml")
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	if r := h.waitForRun(h.fire("restore")); r.Status != "succeeded" || strings.TrimSpace(r.Output) != "restored" {
		t.Errorf("restore: %s\n%s", r.Status, r.Output)
	}
}

func TestRemovingAFileWithEventsRemovesItsEvents(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files")
	h.start()
	offset := fileSize(h.path("kickd.log"))
	if err := os.Remove(h.path("notify.yml")); err != nil {
		t.Fatal(err)
	}
	h.waitForLog(h.path("kickd.log"), offset, "Config reloaded", 1)
	if r := h.kickd("event", "notify"); r.code != 2 || !strings.Contains(r.stderr, `event "notify" is not defined`) {
		t.Errorf("kickd event notify: exit %d\n%s", r.code, r.stderr)
	}
}

func TestCheckRejectsAnEventThatTwoFilesDefine(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files-duplicate")
	r := h.kickd("check")
	if r.code != 1 || !strings.Contains(r.stderr, `nightly.yaml: event "backup": duplicate name, also in config.yaml`) {
		t.Errorf("kickd check: exit %d\n%s", r.code, r.stderr)
	}
}

func TestCheckRejectsASettingsSectionOutsideTheConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "event-files-settings")
	r := h.kickd("check")
	if r.code != 1 || !strings.Contains(r.stderr, "deploy.yaml: line 1: log belongs in the config file") {
		t.Errorf("kickd check: exit %d\n%s", r.code, r.stderr)
	}
}
