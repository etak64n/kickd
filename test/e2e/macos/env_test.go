//go:build e2e && darwin

package macos

import (
	"testing"
)

func TestTheEnvOfTheEventExpandsTheVariablesOfKickd(t *testing.T) {
	t.Parallel()
	h := newHome(t, "env")
	h.start()
	got, want := h.waitForRun(h.fire("greet")).line("greeting"), "hello from "+h.dir
	if got != want {
		t.Errorf("greeting=%s, want %s", got, want)
	}
}

func TestTheParametersOfTheRunReplaceTheEnvOfTheEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "env")
	h.start()
	if got := h.waitForRun(h.fire("deploy", "ref=v2.0")).line("ref"); got != "v2.0" {
		t.Errorf("ref=%s, want v2.0", got)
	}
}

func TestTheEnvOfTheEventGivesAValueThatTheRunLacks(t *testing.T) {
	t.Parallel()
	h := newHome(t, "env")
	h.start()
	if got := h.waitForRun(h.fire("deploy")).line("ref"); got != "main" {
		t.Errorf("ref=%s, want main", got)
	}
}

func TestACommandWithoutWorkdirRunsInTheDirectoryOfTheConfig(t *testing.T) {
	t.Parallel()
	h := newHome(t, "env")
	h.start()
	got, want := h.waitForRun(h.fire("where")).line("dir"), h.dir
	if got != want {
		t.Errorf("dir=%s, want %s", got, want)
	}
}
