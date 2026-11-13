//go:build e2e && windows

package windows

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests in this file start kickd on the database that an earlier
// release left: v0.6.0, whose database has the schema version 4, and
// v0.7.0, whose database has the version 5. They download the releases
// from GitHub, and skip when the download fails, unless KICKD_E2E_ALL is 1.
var earlierReleases = []string{"v0.6.0", "v0.7.0"}

// releases holds the executables of the releases downloaded so far.
var releases = struct {
	sync.Mutex
	exe map[string]string // by version
}{exe: map[string]string{}}

// release returns the kickd executable of the release version for the OS
// and the architecture of the machine, which it downloads from the
// releases of etak64n/kickd on GitHub the first time.
func release(t *testing.T, version string) string {
	t.Helper()
	releases.Lock()
	defer releases.Unlock()
	if exe, ok := releases.exe[version]; ok {
		return exe
	}
	url := fmt.Sprintf("https://github.com/etak64n/kickd/releases/download/%s/kickd-%s-%s.exe", version, runtime.GOOS, runtime.GOARCH)
	exe := filepath.Join(filepath.Dir(kickdPath), "kickd-"+version+".exe")
	err := download(url, exe)
	for attempt := 2; err != nil && attempt <= 3; attempt++ {
		time.Sleep(time.Duration(attempt) * time.Second)
		err = download(url, exe)
	}
	switch {
	case err == nil:
		releases.exe[version] = exe
		return exe
	case os.Getenv("KICKD_E2E_ALL") == "1":
		t.Fatalf("downloading kickd %s: %v", version, err)
	default:
		t.Skipf("downloading kickd %s: %v", version, err)
	}
	return ""
}

// download saves the file at url as the executable path.
func download(url, path string) error {
	client := &http.Client{Timeout: 2 * time.Minute}
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, res.Status)
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(part, path)
}

// earlierRuns are the runs that an earlier release left in its database.
type earlierRuns struct {
	ended       int64 // a run of report that ended
	interrupted int64 // a run of sync that a crash of the agent cut off
	queued      int64 // a run of deploy that kickd event added while no agent ran
}

// leftBy copies testdata/upgrade into a new home, and runs the kickd of the
// release version there, which leaves a run that ended, a run that a crash
// cut off and a run that waits in the database.
func leftBy(t *testing.T, version string) (*home, earlierRuns) {
	t.Helper()
	exe := release(t, version)
	h := newHome(t, "upgrade")
	if r := h.kickdOf(exe, "version"); r.stdout != "kickd "+version+"\n" {
		t.Fatalf("kickd version of the release: exit %d, %q", r.code, r.stdout)
	}
	h.startOf(exe)
	var runs earlierRuns
	runs.ended = h.fireOf(exe, "report")
	h.waitFor("report ends", 30*time.Second, func() bool { return h.showOf(exe, runs.ended).ended() })
	runs.interrupted = h.fireOf(exe, "sync")
	h.waitFor("sync runs", 30*time.Second, func() bool { return h.showOf(exe, runs.interrupted).Status == "running" })
	h.kill()
	runs.queued = h.fireOf(exe, "deploy")
	return h, runs
}

// fireOf runs "event" of the kickd executable exe, and returns the ID of
// the run.
func (h *home) fireOf(exe, event string) int64 {
	h.t.Helper()
	r := h.kickdOf(exe, "event", event, "--json")
	var queued struct {
		ID int64 `json:"runId"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &queued); r.code != 0 || err != nil || queued.ID == 0 {
		h.t.Fatalf("kickd event %s: exit %d, %v\n%s%s", event, r.code, err, r.stdout, r.stderr)
	}
	return queued.ID
}

// showOf returns the run with the ID id, as "show" of the kickd executable
// exe prints it.
func (h *home) showOf(exe string, id int64) run {
	h.t.Helper()
	r := h.kickdOf(exe, "show", strconv.FormatInt(id, 10), "--json")
	var shown run
	if err := json.Unmarshal([]byte(r.stdout), &shown); r.code != 0 || err != nil {
		h.t.Fatalf("kickd show %d: exit %d, %v\n%s%s", id, r.code, err, r.stdout, r.stderr)
	}
	return shown
}

func TestUpgradeRunsTheRunThatWaitedInTheDatabaseOfAnEarlierRelease(t *testing.T) {
	t.Parallel()
	for _, version := range earlierReleases {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			h, runs := leftBy(t, version)
			h.start()
			if r := h.waitForRun(runs.queued); r.Status != "succeeded" || strings.TrimSpace(r.Output) != "deployed" {
				t.Errorf("deploy: %s %s\n%s", r.Status, r.Reason, r.Output)
			}
		})
	}
}

func TestUpgradeRunsTheRunThatACrashCutOffInTheDatabaseOfAnEarlierReleaseAgain(t *testing.T) {
	t.Parallel()
	for _, version := range earlierReleases {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			h, runs := leftBy(t, version)
			h.start()
			rs := h.waitForRuns("sync", 1)
			if last := rs[len(rs)-1]; last.RetryOf != runs.interrupted || last.Attempt != 2 || last.Status != "succeeded" || last.line("attempt") != "2" {
				t.Errorf("the rerun: %+v\n%s", last, last.Output)
			}
		})
	}
}

func TestUpgradeKeepsTheRunsThatEndedInTheDatabaseOfAnEarlierRelease(t *testing.T) {
	t.Parallel()
	for _, version := range earlierReleases {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			h, runs := leftBy(t, version)
			h.start()
			rs := h.runs("report")
			if len(rs) != 1 || rs[0].ID != runs.ended || rs[0].Status != "succeeded" {
				t.Fatalf("the runs of report: %+v", rs)
			}
			if r := h.show(runs.ended); strings.TrimSpace(r.Output) != "reported" {
				t.Errorf("the output of report:\n%s", r.Output)
			}
		})
	}
}
