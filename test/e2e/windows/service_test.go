//go:build e2e && windows

package windows

// The tests in this file change the machine: they install kickd as a
// Windows service, as docs/install-windows.md does, create folders at the
// root of C:, and move the clock. They run only with KICKD_MACHINE_TEST=1,
// which the CI sets on its throwaway machines. They run one at a time,
// because every service listens on port 8787.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// changesTheMachine skips a test that changes the machine, unless
// KICKD_MACHINE_TEST is 1.
func changesTheMachine(t *testing.T) {
	t.Helper()
	if os.Getenv("KICKD_MACHINE_TEST") != "1" {
		t.Skip("changes the machine; set KICKD_MACHINE_TEST=1 to run it")
	}
}

// service is kickd installed as a Windows service, which runs as SYSTEM.
// The tests run in an administrator shell, as the installation guide does.
type service struct {
	t        *testing.T
	config   string
	log      string // where the config puts the log and the database
	database string
}

// windowsService installs kickd as a Windows service with the config of
// testdata/service, and starts it. flags go to kickd service install.
func windowsService(t *testing.T, flags ...string) *service {
	t.Helper()
	changesTheMachine(t)
	s := &service{
		t:        t,
		config:   `C:\ProgramData\kickd\config.yaml`,
		log:      `C:\ProgramData\kickd\kickd.log`,
		database: `C:\ProgramData\kickd\kickd.db`,
	}
	s.removeAtEnd()
	copyTree(t, `testdata\service`, `C:\ProgramData\kickd`)
	s.mustKickd(append([]string{"service", "install"}, flags...)...)
	s.mustKickd("service", "start")
	s.waitForAgent(0)
	return s
}

// removeAtEnd stops and uninstalls the service when the test ends, and
// removes C:\ProgramData\kickd.
func (s *service) removeAtEnd() {
	s.t.Cleanup(func() {
		s.kickd("service", "stop")
		s.kickd("service", "uninstall")
		// The Service Control Manager deletes the service once its process
		// has ended.
		waitFor(s.t, "the service is deleted", 60*time.Second, func() bool {
			return s.run("sc.exe", "query", "kickd").code == 1060
		}, s.describe)
		os.RemoveAll(`C:\ProgramData\kickd`)
	})
}

// run runs a program.
func (s *service) run(name string, args ...string) result {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		s.t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return result{stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()}
}

// kickd runs kickd with args and the config of the service.
func (s *service) kickd(args ...string) result {
	s.t.Helper()
	return s.run(kickdPath, append(args, "-c", s.config)...)
}

// mustKickd runs a kickd subcommand that has to succeed.
func (s *service) mustKickd(args ...string) string {
	s.t.Helper()
	r := s.kickd(args...)
	if r.code != 0 {
		s.t.Fatalf("kickd %s: exit %d\n%s%s\n%s", strings.Join(args, " "), r.code, r.stdout, r.stderr, s.describe())
	}
	return r.stdout
}

// agentPID returns the process ID of the agent when kickd status reports it
// running, and 0 otherwise.
func (s *service) agentPID() int {
	s.t.Helper()
	var st struct {
		Running bool
		Agent   struct{ PID int }
	}
	r := s.kickd("status", "--json")
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &st) != nil || !st.Running {
		return 0
	}
	return st.Agent.PID
}

// waitForAgent waits until an agent other than the process old runs, and
// returns its process ID.
func (s *service) waitForAgent(old int) int {
	s.t.Helper()
	var pid int
	waitFor(s.t, "the agent runs", 60*time.Second, func() bool {
		pid = s.agentPID()
		return pid != 0 && pid != old
	}, s.describe)
	return pid
}

// fire runs kickd event and returns the ID of the run.
func (s *service) fire(event string) int64 {
	s.t.Helper()
	var r run
	if err := json.Unmarshal([]byte(s.mustKickd("event", event, "--json")), &r); err != nil || r.ID == 0 {
		s.t.Fatalf("kickd event %s: %v", event, err)
	}
	return r.ID
}

// show returns the run with the ID id.
func (s *service) show(id int64) run {
	s.t.Helper()
	var r run
	if err := json.Unmarshal([]byte(s.mustKickd("show", strconv.FormatInt(id, 10), "--json")), &r); err != nil {
		s.t.Fatal(err)
	}
	return r
}

// waitForRun waits until the run with the ID id has ended, and returns it.
func (s *service) waitForRun(id int64) run {
	s.t.Helper()
	var r run
	waitFor(s.t, fmt.Sprintf("run %d ends", id), 60*time.Second, func() bool {
		r = s.show(id)
		return r.ended()
	}, s.describe)
	return r
}

// waitForRequest waits until the run of event for the request requestID
// has ended, and returns it.
func (s *service) waitForRequest(event, requestID string) run {
	s.t.Helper()
	var id int64
	waitFor(s.t, "the run of request "+requestID, 60*time.Second, func() bool {
		var rs []run
		if err := json.Unmarshal([]byte(s.mustKickd("runs", "--event", event, "--json")), &rs); err != nil {
			s.t.Fatal(err)
		}
		for _, r := range rs {
			if r.RequestID == requestID {
				id = r.ID
			}
		}
		return id != 0
	}, s.describe)
	return s.waitForRun(id)
}

// describe tells what the Service Control Manager and kickd say about the
// service, for a failure.
func (s *service) describe() string {
	sc := s.run("sc.exe", "queryex", "kickd")
	log, _ := os.ReadFile(s.log)
	lines := strings.Split(string(log), "\n")
	return fmt.Sprintf("sc queryex kickd:\n%s%s\nlog:\n%s", sc.stdout, sc.stderr, strings.Join(lines[max(0, len(lines)-30):], "\n"))
}

// environment returns the variables that the command of the run printed,
// one NAME=value on each line.
func environment(r run) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(r.Output, "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			env[strings.ToUpper(k)] = v
		}
	}
	return env
}

func TestServiceInitWritesTheSystemPathsIntoAConfigOutsideTheHome(t *testing.T) {
	changesTheMachine(t)
	s := &service{t: t, config: `C:\ProgramData\kickd\config.yaml`}
	s.removeAtEnd()
	s.mustKickd("init")
	b, err := os.ReadFile(s.config)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`path: 'C:\ProgramData\kickd\kickd.log'`, `path: 'C:\ProgramData\kickd\kickd.db'`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the config has no %q:\n%s", want, b)
		}
	}
}

func TestServiceRunsCommandsAsSystem(t *testing.T) {
	s := windowsService(t)
	if r := s.waitForRun(s.fire("whoami")); strings.TrimSpace(r.Output) != `nt authority\system` {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceAcceptsAWebhookRequest(t *testing.T) {
	s := windowsService(t)
	res := request(t, "POST", 8787, "/hooks/whoami", "", "Authorization", "Bearer whoami-token")
	var accepted struct{ RequestID string }
	if res.status != 202 || json.Unmarshal([]byte(res.body), &accepted) != nil {
		t.Fatalf("POST /hooks/whoami: %d %s", res.status, res.body)
	}
	if r := s.waitForRequest("whoami", accepted.RequestID); r.Trigger != "webhook" || strings.TrimSpace(r.Output) != `nt authority\system` {
		t.Errorf("whoami: %s by %s\n%s", r.Status, r.Trigger, r.Output)
	}
}

func TestServiceWritesTheLogAndTheDatabaseWhereTheConfigSays(t *testing.T) {
	s := windowsService(t)
	for _, p := range []string{s.log, s.database} {
		if _, err := os.Stat(p); err != nil {
			t.Error(err)
		}
	}
}

func TestServiceStartsAgainAfterACrash(t *testing.T) {
	s := windowsService(t)
	pid := s.agentPID()
	killed := time.Now()
	if r := s.run("taskkill", "/F", "/PID", strconv.Itoa(pid)); r.code != 0 {
		t.Fatalf("taskkill: %s%s", r.stdout, r.stderr)
	}
	s.waitForAgent(pid)
	t.Logf("the Service Control Manager started kickd again %s after the crash", time.Since(killed).Round(time.Second))
}

func TestServiceStatusReportsARunningService(t *testing.T) {
	s := windowsService(t)
	if out := s.mustKickd("service", "status"); strings.TrimSpace(out) != "kickd: running" {
		t.Errorf("kickd service status: %q", out)
	}
}

func TestServiceGivesCommandsTheProfileOfSystem(t *testing.T) {
	s := windowsService(t)
	if got := environment(s.waitForRun(s.fire("show-env")))["USERPROFILE"]; !strings.EqualFold(got, `C:\Windows\system32\config\systemprofile`) {
		t.Errorf("USERPROFILE=%s", got)
	}
}

func TestServiceGivesCommandsTheTempFolderOfWindows(t *testing.T) {
	s := windowsService(t)
	if got := environment(s.waitForRun(s.fire("show-env")))["TEMP"]; !strings.EqualFold(got, `C:\Windows\TEMP`) {
		t.Errorf("TEMP=%s", got)
	}
}

func TestServiceInstallWithUserInstallsTheServiceOfTheSystem(t *testing.T) {
	s := windowsService(t, "--user")
	if r := s.waitForRun(s.fire("whoami")); strings.TrimSpace(r.Output) != `nt authority\system` {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceUninstallDeletesTheService(t *testing.T) {
	s := windowsService(t)
	s.mustKickd("service", "stop")
	s.mustKickd("service", "uninstall")
	waitFor(t, "sc query kickd reports no service", 60*time.Second, func() bool {
		return s.run("sc.exe", "query", "kickd").code == 1060
	}, s.describe)
}
