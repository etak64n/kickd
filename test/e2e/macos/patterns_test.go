//go:build e2e && darwin

package macos

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// The ways of running programs that docs/commands.md shows.

func TestAPythonScriptRunsInItsVirtualEnvironment(t *testing.T) {
	t.Parallel()
	needs(t, "python3")
	h := newHome(t, "patterns")
	venv := exec.Command("python3", "-m", "venv", "--without-pip", ".venv")
	venv.Dir = h.path("reports")
	if out, err := venv.CombinedOutput(); err != nil {
		t.Fatalf("python3 -m venv: %v\n%s", err, out)
	}
	h.start()
	r := h.waitForRun(h.fire("report"))
	if prefix, _ := filepath.EvalSymlinks(r.line("prefix")); r.Status != "succeeded" || r.line("venv") != "True" || prefix != h.path("reports", ".venv") {
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
	if r.Status != "succeeded" || r.line("greet") != "hello" || r.line("program") != filepath.Join(h.inHome("bin"), "greet") {
		t.Errorf("greet: %s\n%s", r.Status, r.Output)
	}
}

func TestPythonReadsThePayloadOnStandardInput(t *testing.T) {
	t.Parallel()
	needs(t, "python3")
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

func TestCatPrintsThePayloadFromStandardInput(t *testing.T) {
	t.Parallel()
	h := newHome(t, "patterns")
	h.start()
	r := h.waitForRun(h.fire("stdin-cat"))
	var payload struct{ Event string }
	if err := json.Unmarshal([]byte(r.Output), &payload); err != nil || payload.Event != "stdin-cat" {
		t.Errorf("stdin-cat: %s\n%s", r.Status, r.Output)
	}
}
