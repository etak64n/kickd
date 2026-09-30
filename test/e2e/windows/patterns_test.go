//go:build e2e && windows

package windows

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ways of running programs that docs/commands.md shows.

func TestAPythonScriptRunsInItsVirtualEnvironment(t *testing.T) {
	t.Parallel()
	needs(t, "python")
	h := newHome(t, "patterns")
	venv := exec.Command("python", "-m", "venv", "--without-pip", ".venv")
	venv.Dir = h.path("reports")
	if out, err := venv.CombinedOutput(); err != nil {
		t.Fatalf("python -m venv: %v\n%s", err, out)
	}
	h.start()
	r := h.waitForRun(h.fire("report"))
	if r.Status != "succeeded" || r.line("venv") != "True" || !strings.EqualFold(r.line("prefix"), h.path("reports", ".venv")) {
		t.Errorf("report: %s\n%s", r.Status, r.Output)
	}
}

func TestNpmRunRunsAScriptOfPackageJSON(t *testing.T) {
	t.Parallel()
	needs(t, "npm")
	h := newHome(t, "patterns")
	h.start()
	if r := h.waitForRun(h.fire("build-site")); r.Status != "succeeded" || r.line("built") != "build" {
		t.Errorf("build-site: %s\n%s", r.Status, r.Output)
	}
}

func TestAProgramIsFoundInThePathOfTheEvent(t *testing.T) {
	t.Parallel()
	h := newHome(t, "patterns")
	h.start()
	r := h.waitForRun(h.fire("greet"))
	if r.Status != "succeeded" || r.line("greet") != "hello" || !strings.EqualFold(r.line("program"), filepath.Join(h.path("bin"), "greet.cmd")) {
		t.Errorf("greet: %s\n%s", r.Status, r.Output)
	}
}

func TestPythonReadsThePayloadOnStandardInput(t *testing.T) {
	t.Parallel()
	needs(t, "python")
	h := newHome(t, "patterns")
	h.start()
	if r := h.waitForRun(h.fire("stdin-python")); r.line("event") != "stdin-python" {
		t.Errorf("stdin-python: %s\n%s", r.Status, r.Output)
	}
}

func TestNodeReadsThePayloadOnStandardInput(t *testing.T) {
	t.Parallel()
	needs(t, "node")
	h := newHome(t, "patterns")
	h.start()
	if r := h.waitForRun(h.fire("stdin-node")); r.line("event") != "stdin-node" {
		t.Errorf("stdin-node: %s\n%s", r.Status, r.Output)
	}
}

func TestFindstrPrintsThePayloadFromStandardInput(t *testing.T) {
	t.Parallel()
	h := newHome(t, "patterns")
	h.start()
	r := h.waitForRun(h.fire("stdin-findstr"))
	var payload struct{ Event string }
	if err := json.Unmarshal([]byte(r.Output), &payload); err != nil || payload.Event != "stdin-findstr" {
		t.Errorf("stdin-findstr: %s\n%s", r.Status, r.Output)
	}
}
