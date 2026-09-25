//go:build usecase

// Package usecase runs the kickd executable the way its users do, on the OS
// that the tests run on. A test writes the config of the README with kickd
// init, replaces the commands with small scripts that print what kickd
// passed them, starts the agent, and fires the events through their
// triggers. The tests build with the tag usecase:
//
//	go test -tags usecase ./test/usecase
package usecase

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// kickd is the executable under test, which TestMain builds.
var kickd string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kickd-usecase-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	kickd = filepath.Join(dir, "kickd")
	if runtime.GOOS == "windows" {
		kickd += ".exe"
	}
	build := exec.Command("go", "build", "-o", kickd, "github.com/etak64n/kickd/cmd/kickd")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building kickd:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// The token of the webhook trigger in the README config.
const token = "replace-with-a-long-random-string"

// home is the setup of one user: a home directory that holds the config of
// the README, the scripts that its events run, the log and the database,
// and the agent that runs on them.
type home struct {
	t      *testing.T
	dir    string // the home directory
	app    string // the working directory of deploy, build and notify
	backup string // the working directory of backup
	cfg    string
	log    string
	db     string
	port   string
	env    []string
	agent  *exec.Cmd
	exited chan struct{}
}

// edit replaces the text old in the config with new; old must appear once.
type edit struct{ old, new string }

// setup writes the README config with kickd init into a new home
// directory, applies the edits, and writes the scripts of the events.
func setup(t *testing.T, edits ...edit) *home {
	t.Helper()
	return setupIn(t, "", edits...)
}

// setupIn is setup with the home directory named name, inside a new
// temporary directory, when name is not empty.
func setupIn(t *testing.T, name string, edits ...edit) *home {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		dir = filepath.Join(dir, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := &home{t: t, dir: dir, cfg: filepath.Join(dir, "kickd.yaml"), port: freePort(t)}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HOME", "USERPROFILE", "KICKD_CONFIG", "LOG_LEVEL", "LOG_FORMAT":
			continue
		}
		h.env = append(h.env, kv)
	}
	// ~ in the config is the home directory: HOME on macOS and Linux, and
	// USERPROFILE on Windows.
	h.env = append(h.env, "HOME="+dir, "USERPROFILE="+dir)

	out := h.must("init")
	m := regexp.MustCompile(`(?m)^log: +(\S.*)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("kickd init did not print the log file:\n%s", out)
	}
	h.log = filepath.Join(dir, strings.TrimSpace(m[1])[2:]) // after ~/ or ~\
	if m := regexp.MustCompile(`(?m)^database: +(\S.*)$`).FindStringSubmatch(out); m != nil {
		h.db = filepath.Join(dir, strings.TrimSpace(m[1])[2:])
	}

	b, err := os.ReadFile(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	// Scripts take the place of the commands of the README, which need
	// other software.
	all := []edit{{"listen: '127.0.0.1:8787'", "listen: '127.0.0.1:" + h.port + "'"}}
	if runtime.GOOS == "windows" {
		// The events of the Windows config work in C:\scripts and C:\app.
		h.backup, h.app = filepath.Join(dir, "scripts"), filepath.Join(dir, "app")
		all = append(all,
			edit{`workdir: 'C:\scripts'`, "workdir: '" + h.backup + "'"},
			edit{`path: 'C:\app\src'`, "path: '" + filepath.Join(h.app, "src") + "'"})
		text = replaceAll(t, text, `workdir: 'C:\app'`, "workdir: '"+h.app+"'", 3)
	} else {
		h.backup, h.app = dir, filepath.Join(dir, "app")
		all = append(all,
			edit{"command: 'rsync -a ~/work/ ~/backup/work/'", "command: './backup.sh'"},
			edit{"command: ['make', 'build']", "command: ['./build.sh']"})
	}
	for _, e := range append(all, edits...) {
		text = replaceAll(t, text, e.old, e.new, 1)
	}
	for _, d := range []string{h.backup, filepath.Join(h.app, "src")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.writeScript(h.backup, "backup")
	for _, name := range []string{"deploy", "build", "notify"} {
		h.writeScript(h.app, name)
	}
	h.write(text)
	h.must("check")
	return h
}

// The script of an event prints what kickd passed it, one value on each
// line, and takes as many seconds as the file sleep-EVENT in the home
// directory says. PowerShell prints UTF-8 only when told to.
const (
	shScript = `#!/bin/sh
echo "event=$KICKD_EVENT"
echo "trigger=$KICKD_TRIGGER"
echo "attempt=$KICKD_ATTEMPT"
echo "msg=$KICKD_DATA_MSG"
echo "file=$KICKD_FILE_PATH"
echo "dir=$(pwd -P)"
if [ -f "$HOME/sleep-$KICKD_EVENT" ]; then sleep "$(cat "$HOME/sleep-$KICKD_EVENT")"; fi
echo done
`
	psScript = `try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch {}
Write-Output "event=$env:KICKD_EVENT"
Write-Output "trigger=$env:KICKD_TRIGGER"
Write-Output "attempt=$env:KICKD_ATTEMPT"
Write-Output "msg=$env:KICKD_DATA_MSG"
Write-Output "file=$env:KICKD_FILE_PATH"
Write-Output "dir=$((Get-Location).Path)"
$sleep = Join-Path $env:USERPROFILE "sleep-$env:KICKD_EVENT"
if (Test-Path -LiteralPath $sleep) { Start-Sleep -Seconds ([int](Get-Content -LiteralPath $sleep)) }
Write-Output 'done'
`
)

func (h *home) writeScript(dir, name string) {
	path, text := filepath.Join(dir, name+".sh"), shScript
	if runtime.GOOS == "windows" {
		path, text = filepath.Join(dir, name+".ps1"), psScript
	}
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// write saves the config file.
func (h *home) write(text string) {
	if err := os.WriteFile(h.cfg, []byte(text), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// sleep makes the script of event take seconds.
func (h *home) sleep(event string, seconds int) {
	if err := os.WriteFile(filepath.Join(h.dir, "sleep-"+event), []byte(strconv.Itoa(seconds)), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func replaceAll(t *testing.T, text, old, new string, n int) string {
	t.Helper()
	if got := strings.Count(text, old); got != n {
		t.Fatalf("the config has %q %d times, want %d:\n%s", old, got, n, text)
	}
	return strings.ReplaceAll(text, old, new)
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// run runs a kickd subcommand on the config of the home, and returns its
// standard output, standard error and exit code.
func (h *home) run(args ...string) (string, string, int) {
	h.t.Helper()
	return h.runRaw(append(args, "-c", h.cfg)...)
}

// runRaw runs kickd with args as they are, in the environment of the home.
func (h *home) runRaw(args ...string) (string, string, int) {
	h.t.Helper()
	cmd := exec.Command(kickd, args...)
	cmd.Env, cmd.Dir = h.env, h.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		h.t.Fatalf("kickd %s: %v", strings.Join(args, " "), err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// must runs a kickd subcommand that has to succeed.
func (h *home) must(args ...string) string {
	h.t.Helper()
	out, errOut, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("kickd %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// start starts the agent and waits until it has loaded the config and
// started the triggers.
func (h *home) start() {
	h.t.Helper()
	offset := fileSize(h.log)
	out, err := os.OpenFile(filepath.Join(h.dir, "agent.out"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command(kickd, "run", "-c", h.cfg)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = h.env, h.dir, out, out
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	h.agent, h.exited = cmd, make(chan struct{})
	go func(done chan struct{}) {
		cmd.Wait()
		out.Close()
		close(done)
	}(h.exited)
	h.t.Cleanup(h.kill)
	h.waitLog(offset, "Config loaded", 30*time.Second)
}

// stop asks the agent to stop, as a service manager does, and waits until
// it exits. Windows has no signal that asks a console program to stop, so
// there the agent is killed.
func (h *home) stop() {
	h.t.Helper()
	if runtime.GOOS == "windows" {
		h.kill()
		return
	}
	if err := h.agent.Process.Signal(syscall.SIGTERM); err != nil {
		h.t.Fatal(err)
	}
	select {
	case <-h.exited:
	case <-time.After(60 * time.Second):
		h.t.Fatal("the agent did not stop")
	}
}

// kill ends the agent at once, as a crash does.
func (h *home) kill() {
	if h.agent == nil {
		return
	}
	select {
	case <-h.exited:
		return
	default:
	}
	h.agent.Process.Kill()
	<-h.exited
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// waitLog waits for a record with the message msg in the log file after
// offset.
func (h *home) waitLog(offset int64, msg string, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(h.log); err == nil && int64(len(b)) > offset {
			for _, line := range strings.Split(string(b[offset:]), "\n") {
				var rec struct{ Message string }
				if json.Unmarshal([]byte(line), &rec) == nil && rec.Message == msg {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			b, _ := os.ReadFile(filepath.Join(h.dir, "agent.out"))
			h.t.Fatalf("the log has no %q after %s\nagent output:\n%s", msg, timeout, b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fire fires an event with kickd event, passing the parameters given as
// KEY=VALUE, and returns the ID of its run.
func (h *home) fire(event string, params ...string) int64 {
	h.t.Helper()
	var r struct {
		ID int64 `json:"runId"`
	}
	if err := json.Unmarshal([]byte(h.must(append([]string{"event", event, "--json"}, params...)...)), &r); err != nil || r.ID == 0 {
		h.t.Fatalf("kickd event %s: %v", event, err)
	}
	return r.ID
}

// show returns the run with the ID id, with its output, as kickd show
// prints it.
func (h *home) show(id int64) record {
	h.t.Helper()
	var r record
	if err := json.Unmarshal([]byte(h.must("show", strconv.FormatInt(id, 10), "--json")), &r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// post sends a webhook request to path, with the token when it is not
// empty, and returns the status code.
func (h *home) post(path, tok string) int {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+h.port+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// record is one run as kickd runs --json prints it.
type record struct {
	ID         int64  `json:"runId"`
	RequestID  string `json:"requestId"`
	Event      string `json:"event"`
	Trigger    string `json:"trigger"`
	Attempt    int    `json:"attempt"`
	RetryOf    int64  `json:"retryOf"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Skipped    int    `json:"skipped"`
	ExitCode   *int   `json:"exitCode"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMs *int64 `json:"durationMs"`
	Output     string `json:"output"`
}

func (r record) String() string {
	return fmt.Sprintf("run %d (%s, %s, attempt %d, %s %s)", r.ID, r.Event, r.Trigger, r.Attempt, r.Status, r.Reason)
}

// started and finished return when kickd started and saw the end of the
// command of the run.
func (r record) started() time.Time  { return parseTime(r.StartedAt) }
func (r record) finished() time.Time { return parseTime(r.FinishedAt) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// runs returns the runs of event, oldest first.
func (h *home) runs(event string) []record {
	h.t.Helper()
	var rs []record
	if err := json.Unmarshal([]byte(h.must("runs", "--event", event, "--limit", "200", "--json")), &rs); err != nil {
		h.t.Fatal(err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

// waitRuns waits until ok accepts the runs of event.
func (h *home) waitRuns(event, what string, timeout time.Duration, ok func([]record) bool) []record {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rs := h.runs(event)
		if ok(rs) {
			return rs
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s: the runs of %s are %v", what, event, rs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitRun waits until the run with the ID id has ended, and returns it with
// its output.
func (h *home) waitRun(id int64, timeout time.Duration) record {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r := h.show(id)
		if final(r) || time.Now().After(deadline) {
			return r
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// find returns the run with the ID id.
func find(rs []record, id int64) (record, bool) {
	for _, r := range rs {
		if r.ID == id {
			return r, true
		}
	}
	return record{}, false
}

// report is what the script of a run printed: the value after KEY= on the
// first line that starts with it.
type report map[string]string

func parseReport(output string) report {
	rep := report{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(strings.TrimPrefix(line, "\uFEFF"), "\r")
		if k, v, ok := strings.Cut(line, "="); ok {
			if _, seen := rep[k]; !seen {
				rep[k] = v
			}
		}
	}
	return rep
}

// checkRun checks that the run succeeded, and that its script ran in dir
// with the event and trigger of the run.
func (h *home) checkRun(r record, dir string) report {
	h.t.Helper()
	r = h.show(r.ID)
	rep := parseReport(r.Output)
	if r.Status != "succeeded" || rep["event"] == "" || !strings.Contains(r.Output, "done") {
		h.t.Fatalf("%v printed:\n%s", r, r.Output)
	}
	if rep["event"] != r.Event || rep["trigger"] != r.Trigger || rep["attempt"] != strconv.Itoa(r.Attempt) {
		h.t.Errorf("%v: the script got event %s, trigger %s, attempt %s", r, rep["event"], rep["trigger"], rep["attempt"])
	}
	if !samePath(rep["dir"], dir) {
		h.t.Errorf("%v: the script ran in %s, want %s", r, rep["dir"], dir)
	}
	return rep
}

// samePath reports whether a and b name the same directory, after links
// and, on Windows, short names and case.
func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// final reports whether the run has ended, and no later attempt follows it.
func final(r record) bool {
	switch r.Status {
	case "", "queued", "running", "interrupted", "retried":
		return false
	}
	return true
}

// allFinal reports whether every run has ended.
func allFinal(rs []record) bool {
	for _, r := range rs {
		if !final(r) {
			return false
		}
	}
	return true
}

// status returns the status of the run with the ID id.
func status(rs []record, id int64) string {
	r, _ := find(rs, id)
	return r.Status
}
