//go:build usecase

package usecase

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// kind is one kind of command that an event runs: commands of the OS in a
// string that the shell runs, a script of an interpreter, or a compiled
// program. Every kind does the same work, so the test checks them the same
// way: it prints the variables that kickd passed and the payload file,
// writes a line to standard error, and exits with the code in the
// parameter code.
type kind struct {
	name    string
	goos    string   // "unix" or "windows"; empty for every OS
	need    []string // programs that must be in PATH
	file    string   // the script, in the working directory of the event
	script  string
	build   []string // builds the program from the script in the working directory
	command string   // the command of the event, in YAML
}

func (k kind) runsOn(goos string) bool {
	return k.goos == "" || (k.goos == "windows") == (goos == "windows")
}

// missing returns a program that the kind needs and PATH does not have.
func (k kind) missing() string {
	for _, p := range k.need {
		if _, err := exec.LookPath(p); err != nil {
			return p
		}
	}
	return ""
}

func kinds() []kind {
	python, exe := "python3", ""
	if runtime.GOOS == "windows" {
		python, exe = "python", ".exe"
	}
	return []kind{
		{name: "commands", goos: "unix",
			command: `'echo "event=$KICKD_EVENT trigger=$KICKD_TRIGGER run=$KICKD_RUN_ID msg=$KICKD_DATA_MSG dir=$(pwd -P)"; echo "payload=$(cat "$KICKD_PAYLOAD_FILE")"; echo "to stderr" >&2; exit "$KICKD_DATA_CODE"'`},
		{name: "commands", goos: "windows",
			command: `'echo event=%KICKD_EVENT% trigger=%KICKD_TRIGGER% run=%KICKD_RUN_ID% msg=%KICKD_DATA_MSG% dir=%CD%& set /p "=payload=" <nul& type "%KICKD_PAYLOAD_FILE%"& echo.& echo to stderr 1>&2& exit /b %KICKD_DATA_CODE%'`},
		{name: "sh", goos: "unix", file: "report.sh", command: `['./report.sh']`, script: `#!/bin/sh
echo "event=$KICKD_EVENT trigger=$KICKD_TRIGGER run=$KICKD_RUN_ID msg=$KICKD_DATA_MSG dir=$(pwd -P)"
echo "payload=$(cat "$KICKD_PAYLOAD_FILE")"
echo "to stderr" >&2
exit "$KICKD_DATA_CODE"
`},
		{name: "cmd", goos: "windows", file: "report.cmd", command: `'report.cmd'`, script: strings.ReplaceAll(`@echo off
echo event=%KICKD_EVENT% trigger=%KICKD_TRIGGER% run=%KICKD_RUN_ID% msg=%KICKD_DATA_MSG% dir=%CD%
set /p "=payload=" <nul
type "%KICKD_PAYLOAD_FILE%"
echo.
echo to stderr 1>&2
exit /b %KICKD_DATA_CODE%
`, "\n", "\r\n")},
		{name: "python", need: []string{python}, file: "report.py", command: `['` + python + `', 'report.py']`, script: `import os
import sys

env = os.environ
print(f"event={env['KICKD_EVENT']} trigger={env['KICKD_TRIGGER']} run={env['KICKD_RUN_ID']} msg={env['KICKD_DATA_MSG']} dir={os.getcwd()}")
with open(env["KICKD_PAYLOAD_FILE"], encoding="utf-8") as f:
    print("payload=" + f.read())
print("to stderr", file=sys.stderr)
sys.exit(int(env["KICKD_DATA_CODE"]))
`},
		{name: "node", need: []string{"node"}, file: "report.js", command: `['node', 'report.js']`, script: `const fs = require('node:fs');

const env = process.env;
console.log(` + "`event=${env.KICKD_EVENT} trigger=${env.KICKD_TRIGGER} run=${env.KICKD_RUN_ID} msg=${env.KICKD_DATA_MSG} dir=${process.cwd()}`" + `);
console.log('payload=' + fs.readFileSync(env.KICKD_PAYLOAD_FILE, 'utf8'));
console.error('to stderr');
process.exitCode = Number(env.KICKD_DATA_CODE);
`},
		{name: "ruby", need: []string{"ruby"}, file: "report.rb", command: `['ruby', 'report.rb']`, script: `env = ENV
puts "event=#{env['KICKD_EVENT']} trigger=#{env['KICKD_TRIGGER']} run=#{env['KICKD_RUN_ID']} msg=#{env['KICKD_DATA_MSG']} dir=#{Dir.pwd}"
puts 'payload=' + File.read(env['KICKD_PAYLOAD_FILE'])
warn 'to stderr'
exit Integer(env['KICKD_DATA_CODE'])
`},
		{name: "perl", need: []string{"perl"}, file: "report.pl", command: `['perl', 'report.pl']`, script: `use strict;
use warnings;
use Cwd qw(getcwd);

my $payload = do { local $/; open my $f, '<', $ENV{KICKD_PAYLOAD_FILE} or die "$!"; <$f> };
print "event=$ENV{KICKD_EVENT} trigger=$ENV{KICKD_TRIGGER} run=$ENV{KICKD_RUN_ID} msg=$ENV{KICKD_DATA_MSG} dir=" . getcwd() . "\n";
print "payload=$payload\n";
print STDERR "to stderr\n";
exit $ENV{KICKD_DATA_CODE};
`},
		{name: "rust", need: []string{"rustc"}, file: "report.rs", build: []string{"rustc", "-o", "report" + exe, "report.rs"},
			command: `['./report` + exe + `']`, script: `use std::{env, fs, process};

fn main() {
    let var = |k: &str| env::var(k).unwrap_or_default();
    let dir = env::current_dir().unwrap();
    println!("event={} trigger={} run={} msg={} dir={}", var("KICKD_EVENT"), var("KICKD_TRIGGER"), var("KICKD_RUN_ID"), var("KICKD_DATA_MSG"), dir.display());
    println!("payload={}", fs::read_to_string(var("KICKD_PAYLOAD_FILE")).unwrap());
    eprintln!("to stderr");
    process::exit(var("KICKD_DATA_CODE").parse().unwrap_or(1));
}
`},
		{name: "pwsh", need: []string{"pwsh"}, file: "report.ps1", command: `['pwsh', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'report.ps1']`, script: psReport},
		{name: "powershell", goos: "windows", need: []string{"powershell"}, file: "report.ps1",
			command: `['powershell', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', 'report.ps1']`, script: psReport},
	}
}

const psReport = `$dir = (Get-Location).Path
Write-Output "event=$env:KICKD_EVENT trigger=$env:KICKD_TRIGGER run=$env:KICKD_RUN_ID msg=$env:KICKD_DATA_MSG dir=$dir"
Write-Output ('payload=' + (Get-Content -Raw -LiteralPath $env:KICKD_PAYLOAD_FILE))
[Console]::Error.WriteLine('to stderr')
exit [int]$env:KICKD_DATA_CODE
`

var (
	commandLine = regexp.MustCompile(`event=(\S*) trigger=(\S*) run=(\S*) msg=(\S*) dir=(.*)`)
	payloadLine = regexp.MustCompile(`(?m)^payload=(.*)$`)
)

// Commands of every kind get the variables and the payload file of their
// run, run in the working directory of their event, and report their exit
// code and both output streams. With KICKD_USECASE_ALL=1, as in the CI,
// every kind for the OS must run; otherwise a kind whose program is missing
// is skipped.
func TestUseCaseCommands(t *testing.T) {
	t.Parallel()
	h := setup(t)
	var added strings.Builder
	var ks []kind
	for _, k := range kinds() {
		if !k.runsOn(runtime.GOOS) {
			continue
		}
		if p := k.missing(); p != "" {
			if os.Getenv("KICKD_USECASE_ALL") == "1" {
				t.Errorf("%s: %s is not in PATH", k.name, p)
			} else {
				t.Logf("%s skipped: %s is not in PATH", k.name, p)
			}
			continue
		}
		dir := filepath.Join(h.dir, k.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if k.file != "" {
			if err := os.WriteFile(filepath.Join(dir, k.file), []byte(k.script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if k.build != nil {
			build := exec.Command(k.build[0], k.build[1:]...)
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("%s: building failed: %v\n%s", k.name, err, out)
			}
		}
		// Both firings of an event must run, so they queue.
		added.WriteString("\n  - name: run-" + k.name + "\n    command: " + k.command + "\n    workdir: '" + dir + "'\n" +
			"    concurrency: queue\n    params:\n      - name: msg\n      - name: code\n        default: '0'\n")
		ks = append(ks, k)
	}
	b, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.write(string(b) + added.String())
	h.must("check")
	h.start()

	type pair struct {
		k       kind
		ok, bad int64
	}
	var fired []pair
	for _, k := range ks {
		ok := h.fire("run-"+k.name, "msg=hello-"+k.name)
		bad := h.fire("run-"+k.name, "msg=failing", "code=3")
		fired = append(fired, pair{k, ok, bad})
	}
	for _, f := range fired {
		dir := filepath.Join(h.dir, f.k.name)
		r := h.waitRun(f.ok, 120*time.Second)
		if r.Status != "succeeded" || r.ExitCode == nil || *r.ExitCode != 0 {
			t.Errorf("%s: %v, exit code %v, printed:\n%s", f.k.name, r, r.ExitCode, r.Output)
			continue
		}
		m := commandLine.FindStringSubmatch(r.Output)
		if m == nil || !strings.Contains(r.Output, "to stderr") {
			t.Errorf("%s: %v printed:\n%s", f.k.name, r, r.Output)
			continue
		}
		if m[1] != "run-"+f.k.name || m[2] != "manual" || m[3] != strconv.FormatInt(r.ID, 10) || m[4] != "hello-"+f.k.name {
			t.Errorf("%s: got event %s, trigger %s, run %s, msg %s, want run-%s, manual, %d, hello-%s",
				f.k.name, m[1], m[2], m[3], m[4], f.k.name, r.ID, f.k.name)
		}
		if got := strings.TrimSpace(m[5]); !samePath(got, dir) {
			t.Errorf("%s: ran in %s, want %s", f.k.name, got, dir)
		}
		var payload struct {
			Event   string            `json:"event"`
			RunID   int64             `json:"runId"`
			Trigger string            `json:"trigger"`
			Data    map[string]string `json:"data"`
		}
		p := payloadLine.FindStringSubmatch(r.Output)
		if p == nil || json.Unmarshal([]byte(strings.TrimSpace(p[1])), &payload) != nil {
			t.Errorf("%s: no payload JSON in the output:\n%s", f.k.name, r.Output)
		} else if payload.Event != "run-"+f.k.name || payload.RunID != r.ID || payload.Trigger != "manual" || payload.Data["msg"] != "hello-"+f.k.name {
			t.Errorf("%s: the payload file holds %+v", f.k.name, payload)
		}

		r = h.waitRun(f.bad, 120*time.Second)
		if r.Status != "failed" || r.ExitCode == nil || *r.ExitCode != 3 {
			t.Errorf("%s: a command that exits with 3: %v, exit code %v, printed:\n%s", f.k.name, r, r.ExitCode, r.Output)
		}
	}
	h.stop()
}
