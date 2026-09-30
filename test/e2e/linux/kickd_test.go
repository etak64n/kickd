//go:build e2e && linux

// Package linux runs the kickd executable on Linux the way its users do.
// Each test copies a directory of testdata into a new home directory. The
// directory .kickd in it holds the config file, config.yaml, and the
// scripts of its events. The test starts kickd there, fires events, and
// checks the runs that kickd records. The tests build
// with the tag e2e, and test/e2e/README.md lists them.
package linux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"text/template"
	"time"
)

// kickdPath is the kickd executable that the tests run, built by TestMain.
var kickdPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kickd-linux-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// The service definitions record the path that os.Executable reports,
	// which has no symbolic links.
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	kickdPath = filepath.Join(dir, "kickd")
	build := exec.Command("go", "build", "-o", kickdPath, "github.com/etak64n/kickd/cmd/kickd")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building kickd:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// home is the home directory of one user, with the config of kickd and
// the scripts of its events in the directory .kickd, and the kickd agent
// that runs on them.
type home struct {
	t       *testing.T
	homeDir string    // the home directory
	dir     string    // the directory .kickd of the home, with the config file
	at      time.Time // the time that {{at}} in config.yaml stands for
	env     []string
	agent   *exec.Cmd
	exited  chan struct{}
}

// newHome copies testdata/<fixture> into a new home directory. In
// .kickd/config.yaml, the template action at "ZONE" becomes a cron
// schedule for 25 seconds from now in that time zone.
func newHome(t *testing.T, fixture string) *home {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &home{t: t, homeDir: dir, dir: filepath.Join(dir, ".kickd"), at: time.Now().UTC().Truncate(time.Second).Add(25 * time.Second)}
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "HOME", "LOG_LEVEL", "LOG_FORMAT":
			continue
		}
		h.env = append(h.env, kv)
	}
	h.env = append(h.env, "HOME="+dir)
	if fixture != "" {
		h.copy(filepath.Join("testdata", fixture))
	}
	return h
}

// copy copies the files under src into the home directory, and fills in
// the times in the config file.
func (h *home) copy(src string) {
	h.t.Helper()
	copyTree(h.t, src, h.homeDir)
	b, err := os.ReadFile(h.config())
	if err == nil && bytes.Contains(b, []byte("{{")) {
		h.write("config.yaml", string(h.fillTimes(b)))
	}
}

// copyTree copies the files under src into dst, keeping their permissions.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (h *home) fillTimes(b []byte) []byte {
	h.t.Helper()
	tmpl, err := template.New("config.yaml").Funcs(template.FuncMap{
		"at": func(zone string) (string, error) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				return "", err
			}
			t := h.at.In(loc)
			return fmt.Sprintf("%d %d %d * * *", t.Second(), t.Minute(), t.Hour()), nil
		},
	}).Parse(string(b))
	if err != nil {
		h.t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, nil); err != nil {
		h.t.Fatal(err)
	}
	return out.Bytes()
}

// path returns the path of name in the directory .kickd, where the config
// file is.
func (h *home) path(name ...string) string {
	return filepath.Join(append([]string{h.dir}, name...)...)
}

// inHome returns the path of name in the home directory.
func (h *home) inHome(name ...string) string {
	return filepath.Join(append([]string{h.homeDir}, name...)...)
}

// config is the config file that kickd reads for the user of the home.
func (h *home) config() string { return h.path("config.yaml") }

// result is what a kickd subcommand printed, and its exit code.
type result struct {
	stdout, stderr string
	code           int
}

// kickd runs kickd with args in the home directory, as the user of the
// home.
func (h *home) kickd(args ...string) result {
	h.t.Helper()
	cmd := exec.Command(kickdPath, args...)
	cmd.Env, cmd.Dir = h.env, h.homeDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		h.t.Fatalf("kickd %s: %v", strings.Join(args, " "), err)
	}
	return result{stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()}
}

// must runs a kickd subcommand that has to succeed, and returns its output.
func (h *home) must(args ...string) string {
	h.t.Helper()
	r := h.kickd(args...)
	if r.code != 0 {
		h.t.Fatalf("kickd %s: exit %d\n%s%s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// logFile is where kickd check says the log goes.
func (h *home) logFile() string {
	h.t.Helper()
	m := regexp.MustCompile(`(?m)^log: (.*) \(level=`).FindStringSubmatch(h.must("check"))
	if m == nil {
		h.t.Fatal("kickd check printed no log file")
	}
	return m[1]
}

// start runs "kickd run" and waits until the agent has loaded the config.
func (h *home) start() {
	h.t.Helper()
	log := h.logFile()
	offset := fileSize(log)
	out, err := os.OpenFile(h.inHome("agent.out"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command(kickdPath, "run")
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = h.env, h.homeDir, out, out
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
	h.waitForLog(log, offset, "Config loaded", 1)
}

// stop sends SIGTERM to the agent, as a service manager does, and waits
// until it exits.
func (h *home) stop() {
	h.t.Helper()
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

// waitForLog waits for n records with the message msg in the log file
// after offset.
func (h *home) waitForLog(log string, offset int64, msg string, n int) {
	h.t.Helper()
	h.waitFor(fmt.Sprintf("%d log records %q", n, msg), 30*time.Second, func() bool {
		b, err := os.ReadFile(log)
		return err == nil && int64(len(b)) >= offset && records(string(b[offset:]), msg) >= n
	})
}

// records counts the records with the message msg in the text of a log.
func records(text, msg string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		var rec struct{ Message string }
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Message == msg {
			count++
		}
	}
	return count
}

// waitFor waits until ok reports true.
func (h *home) waitFor(what string, timeout time.Duration, ok func() bool) {
	h.t.Helper()
	waitFor(h.t, what, timeout, ok, func() string {
		b, _ := os.ReadFile(h.inHome("agent.out"))
		return "agent output:\n" + string(b)
	})
}

// waitFor waits until ok reports true. On a timeout, it fails the test with
// what describe returns.
func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool, describe func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not after %s\n%s", what, timeout, describe())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// fire runs "kickd event" and returns the ID of the run.
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

// run is one run, as kickd prints it.
type run struct {
	ID         int64          `json:"runId"`
	RequestID  string         `json:"requestId"`
	Event      string         `json:"event"`
	Trigger    string         `json:"trigger"`
	TriggerID  string         `json:"triggerId"`
	Attempt    int            `json:"attempt"`
	RetryOf    int64          `json:"retryOf"`
	Status     string         `json:"status"`
	Reason     string         `json:"reason"`
	ExitCode   *int           `json:"exitCode"`
	Skipped    int            `json:"skipped"`
	StartedAt  string         `json:"startedAt"`
	FinishedAt string         `json:"finishedAt"`
	DurationMs int64          `json:"durationMs"`
	Output     string         `json:"output"`
	Payload    map[string]any `json:"payload"`
}

// ended reports whether the run has ended, and no rerun follows it.
func (r run) ended() bool {
	switch r.Status {
	case "", "queued", "running", "interrupted", "retried":
		return false
	}
	return true
}

func (r run) started() time.Time  { t, _ := time.Parse(time.RFC3339, r.StartedAt); return t }
func (r run) finished() time.Time { t, _ := time.Parse(time.RFC3339, r.FinishedAt); return t }

// line returns the value that the command printed as KEY=value on the first
// line that starts with key and "=".
func (r run) line(key string) string {
	for _, l := range strings.Split(r.Output, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), key+"="); ok {
			return v
		}
	}
	return ""
}

// runs returns the runs of event, oldest first.
func (h *home) runs(event string) []run {
	h.t.Helper()
	var rs []run
	if err := json.Unmarshal([]byte(h.must("history", "--event", event, "--limit", "200", "--json")), &rs); err != nil {
		h.t.Fatal(err)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

// show returns the run with the ID id, with its output and its payload.
func (h *home) show(id int64) run {
	h.t.Helper()
	var r run
	if err := json.Unmarshal([]byte(h.must("show", strconv.FormatInt(id, 10), "--json")), &r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// waitForRuns waits until event has n runs that have ended, and returns
// them with their output, oldest first.
func (h *home) waitForRuns(event string, n int) []run {
	h.t.Helper()
	var rs []run
	h.waitFor(fmt.Sprintf("%d ended runs of %s", n, event), 90*time.Second, func() bool {
		rs = h.runs(event)
		ended := 0
		for _, r := range rs {
			if r.ended() {
				ended++
			}
		}
		return ended >= n
	})
	for i := range rs {
		rs[i] = h.show(rs[i].ID)
	}
	return rs
}

// waitForRun waits until the run with the ID id has ended, and returns it.
func (h *home) waitForRun(id int64) run {
	h.t.Helper()
	var r run
	h.waitFor(fmt.Sprintf("run %d ends", id), 90*time.Second, func() bool {
		r = h.show(id)
		return r.ended()
	})
	return r
}

// response is the answer of the webhook server.
type response struct {
	status int
	body   string
	header http.Header
}

// request sends an HTTP request to the webhook server at port, with headers
// given as name and value.
func request(t *testing.T, method string, port int, path, body string, headers ...string) response {
	t.Helper()
	req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, string(b), res.Header}
}

// save replaces config.yaml with the file name in the directory .kickd,
// as saving an edited config does.
func (h *home) save(name string) {
	h.t.Helper()
	b, err := os.ReadFile(h.path(name))
	if err != nil {
		h.t.Fatal(err)
	}
	h.write("config.yaml", string(b))
}

// place copies the file src of the directory .kickd to dst, as saving a
// new file does.
func (h *home) place(src, dst string) {
	h.t.Helper()
	b, err := os.ReadFile(h.path(src))
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(dst, string(b))
}

// writeFile writes text to the file at path.
func (h *home) writeFile(path, text string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// write writes text to the file name in the directory .kickd.
func (h *home) write(name, text string) {
	h.t.Helper()
	h.writeFile(h.path(name), text)
}
