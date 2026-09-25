//go:build usecase

package usecase

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The ways of running programs that docs/commands.md describes: a Python
// virtual environment, npm run, a program found through the PATH of the
// event, and the payload on standard input.
func TestUseCaseDocumentedPatterns(t *testing.T) {
	t.Parallel()
	h := setup(t)
	windows := runtime.GOOS == "windows"
	python := "python3"
	if windows {
		python = "python"
	}
	var added strings.Builder
	add := func(yaml string) { added.WriteString("\n" + yaml + "    triggers:\n      - type: manual\n") }
	have := func(programs ...string) bool {
		for _, p := range programs {
			if _, err := exec.LookPath(p); err != nil {
				if os.Getenv("KICKD_USECASE_ALL") == "1" {
					t.Errorf("%s is not in PATH", p)
				} else {
					t.Logf("skipped the parts that need %s, which is not in PATH", p)
				}
				return false
			}
		}
		return true
	}
	var events []string

	// A virtual environment needs no activation: its own python runs the
	// script with the packages of the environment.
	reports := filepath.Join(h.dir, "reports")
	if have(python) {
		mkdir(t, reports)
		venv := exec.Command(python, "-m", "venv", "--without-pip", ".venv")
		venv.Dir = reports
		if out, err := venv.CombinedOutput(); err != nil {
			t.Fatalf("python -m venv: %v\n%s", err, out)
		}
		writeText(t, filepath.Join(reports, "report.py"), "import sys\nprint(f\"venv={sys.prefix != sys.base_prefix} prefix={sys.prefix}\")\n")
		command := `['.venv/bin/python', 'report.py']`
		if windows {
			command = `['.venv\Scripts\python.exe', 'report.py']`
		}
		add("  - name: report\n    command: " + command + "\n    workdir: '~/reports'\n")
		events = append(events, "report")
	}

	// npm is a script of Node.js, and on Windows the batch file npm.cmd,
	// which runs through cmd, so the command is a string there.
	if have("node", "npm") {
		site := filepath.Join(h.dir, "site")
		mkdir(t, site)
		writeText(t, filepath.Join(site, "package.json"), `{"name": "site", "private": true, "scripts": {"report": "node report.js"}}`+"\n")
		writeText(t, filepath.Join(site, "report.js"), "console.log(`npm-report event=${process.env.KICKD_EVENT} script=${process.env.npm_lifecycle_event}`);\n")
		command := `['npm', 'run', 'report']`
		if windows {
			command = `'npm run report'`
		}
		add("  - name: build-site\n    command: " + command + "\n    workdir: '~/site'\n")
		events = append(events, "build-site")
	}

	// A program without a path is looked up in the PATH of the command,
	// which env of the event extends.
	tools := filepath.Join(h.dir, "tools", "bin")
	if have("go") {
		src := filepath.Join(t.TempDir(), "greet.go")
		writeText(t, src, "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\nfunc main() {\n\texe, _ := os.Executable()\n\tfmt.Printf(\"greet %s from %s\\n\", strings.Join(os.Args[1:], \" \"), exe)\n}\n")
		exe := filepath.Join(tools, "greet")
		if windows {
			exe += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", exe, src).CombinedOutput(); err != nil {
			t.Fatalf("building greet: %v\n%s", err, out)
		}
		path := "'${HOME}/tools/bin:${PATH}'"
		if windows {
			path = `'${USERPROFILE}\tools\bin;${PATH}'`
		}
		add("  - name: greet\n    command: ['greet', 'hello']\n    env:\n      PATH: " + path + "\n")
		events = append(events, "greet")
	}

	// With stdin: payload, the command reads the payload JSON on standard
	// input.
	if have(python) {
		add("  - name: stdin-python\n    command: ['" + python + `', '-c', 'import json, sys; print("stdin-event=" + json.load(sys.stdin)["event"])']` + "\n    stdin: payload\n")
		events = append(events, "stdin-python")
	}
	if have("node") {
		add("  - name: stdin-node\n    command: ['node', '-e', 'let s = \"\"; process.stdin.on(\"data\", d => s += d).on(\"end\", () => console.log(\"stdin-event=\" + JSON.parse(s).event))']\n    stdin: payload\n")
		events = append(events, "stdin-node")
	}
	reader := `['cat']`
	if windows {
		reader = `['findstr', '^']`
	}
	add("  - name: stdin-text\n    command: " + reader + "\n    stdin: payload\n")
	events = append(events, "stdin-text")

	b, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.write(string(b) + added.String())
	h.must("check")
	h.start()
	ids := map[string]int64{}
	for _, e := range events {
		ids[e] = h.fire(e)
	}
	for _, e := range events {
		r := h.waitRun(ids[e], 120*time.Second)
		if r.Status != "succeeded" {
			t.Errorf("%s: %v\n%s", e, r, r.Output)
			continue
		}
		rep := parseReport(strings.ReplaceAll(r.Output, " ", "\n"))
		switch e {
		case "report":
			if rep["venv"] != "True" || !samePath(rep["prefix"], filepath.Join(reports, ".venv")) {
				t.Errorf("report ran outside the virtual environment:\n%s", r.Output)
			}
		case "build-site":
			if rep["event"] != "build-site" || rep["script"] != "report" {
				t.Errorf("npm run report printed:\n%s", r.Output)
			}
		case "greet":
			if !strings.Contains(r.Output, "greet hello from ") || !strings.Contains(r.Output, filepath.Join("tools", "bin", "greet")) {
				t.Errorf("greet was not found in the PATH of the event:\n%s", r.Output)
			}
		case "stdin-python", "stdin-node":
			if rep["stdin-event"] != e {
				t.Errorf("%s read no payload on standard input:\n%s", e, r.Output)
			}
		case "stdin-text":
			var payload struct{ Event string }
			if json.Unmarshal([]byte(strings.TrimSpace(r.Output)), &payload) != nil || payload.Event != e {
				t.Errorf("%s printed:\n%s", e, r.Output)
			}
		}
	}
	h.stop()
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
