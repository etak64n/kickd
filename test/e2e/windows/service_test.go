//go:build e2e && windows

package windows

// The tests in this file change the machine: they install kickd as a
// Windows service, as docs/src/content/docs/getting-started/install-windows.md
// does, create folders at the root of C:, and move the clock. They run only
// with KICKD_MACHINE_TEST=1, which the CI sets on its throwaway machines.
// They run one at a time, because every service listens on port 9787.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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
// testdata/service, and starts it.
func windowsService(t *testing.T) *service {
	t.Helper()
	changesTheMachine(t)
	s := &service{
		t:        t,
		config:   `C:\ProgramData\kickd\config.yaml`,
		log:      `C:\ProgramData\kickd\kickd.log`,
		database: `C:\ProgramData\kickd\kickd.db`,
	}
	s.removeAtEnd()
	makeConfigFolder(t, `C:\ProgramData\kickd`)
	copyTree(t, `testdata\service`, `C:\ProgramData\kickd`)
	s.mustKickd("service", "install")
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

// kickd runs kickd with args as an administrator, so that it reads the
// config of the service.
func (s *service) kickd(args ...string) result {
	s.t.Helper()
	return s.run(kickdPath, args...)
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
		if err := json.Unmarshal([]byte(s.mustKickd("history", "--event", event, "--json")), &rs); err != nil {
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

func TestServiceInitOfAnAdministratorWritesTheConfigForTheWholeMachine(t *testing.T) {
	changesTheMachine(t)
	s := &service{t: t, config: `C:\ProgramData\kickd\config.yaml`}
	s.removeAtEnd()
	if out := s.mustKickd("init"); !strings.HasPrefix(out, "wrote "+s.config+"\n") {
		t.Errorf("kickd init:\n%s", out)
	}
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

// accessList returns the access control list of the file or folder at
// path, in the form of a security descriptor string, such as
// D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA).
func accessList(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

// trustees returns the accounts to which the rules of the access control
// list acl, in the form of a security descriptor string, give or deny
// rights, such as "SY BA" for SYSTEM and the Administrators group.
func trustees(acl string) string {
	var out []string
	for _, m := range regexp.MustCompile(`\((?:[^;()]*;){5}([^;()]+)\)`).FindAllStringSubmatch(acl, -1) {
		out = append(out, m[1])
	}
	return strings.Join(out, " ")
}

func TestServiceInitOfAnAdministratorLetsOnlySystemAndAdministratorsIntoTheConfigFolder(t *testing.T) {
	changesTheMachine(t)
	s := &service{t: t, config: `C:\ProgramData\kickd\config.yaml`}
	s.removeAtEnd()
	s.mustKickd("init")
	if got, want := accessList(t, `C:\ProgramData\kickd`), "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"; got != want {
		t.Errorf(`the access control list of C:\ProgramData\kickd is %s, want %s`, got, want)
	}
	for _, p := range []string{s.config, `C:\ProgramData\kickd\event.example.yaml`} {
		if got := accessList(t, p); trustees(got) != "SY BA" {
			t.Errorf("the access control list of %s is %s, want rules for SY and BA alone", p, got)
		}
	}
}

// usersCanChange writes the config for the whole machine as an
// administrator does, with kickd init and then the config of
// testdata/service, which kickd check accepts. It then gives the Users
// group write access to the config folder and the files in it with
// icacls.
func usersCanChange(t *testing.T) *service {
	t.Helper()
	changesTheMachine(t)
	s := &service{t: t, config: `C:\ProgramData\kickd\config.yaml`, log: `C:\ProgramData\kickd\kickd.log`}
	s.removeAtEnd()
	s.mustKickd("init", "--no-examples")
	copyTree(t, `testdata\service`, `C:\ProgramData\kickd`)
	s.mustKickd("check")
	if r := s.run("icacls", `C:\ProgramData\kickd`, "/grant", "*S-1-5-32-545:(OI)(CI)W"); r.code != 0 {
		t.Fatalf("icacls: exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
	return s
}

func TestServiceCheckOfAnAdministratorRefusesAConfigThatUsersCanChange(t *testing.T) {
	s := usersCanChange(t)
	r := s.kickd("check")
	// The name of the group depends on the language of Windows, and its
	// security identifier does not.
	for _, want := range []string{`C:\ProgramData\kickd can be changed by `, "(S-1-5-32-545); give only SYSTEM and the Administrators group access to it"} {
		if r.code == 0 || !strings.Contains(r.stderr, want) {
			t.Errorf("kickd check: exit %d, want an error with %q\n%s%s", r.code, want, r.stdout, r.stderr)
		}
	}
}

func TestServiceCheckOfAnAdministratorPrintsTheCommandsThatFixTheConfig(t *testing.T) {
	s := usersCanChange(t)
	r := s.kickd("check")
	fixes := regexp.MustCompile(`(?m)icacls ".*$`).FindAllString(r.stderr, -1)
	if r.code == 0 || len(fixes) == 0 {
		t.Fatalf("kickd check: exit %d, want icacls commands\n%s%s", r.code, r.stdout, r.stderr)
	}
	// An administrator runs the commands in PowerShell, as the
	// installation guide does.
	for _, fix := range fixes {
		if r := s.run("powershell", "-NoProfile", "-Command", fix); r.code != 0 {
			t.Fatalf("%s: exit %d\n%s%s", fix, r.code, r.stdout, r.stderr)
		}
	}
	if r := s.kickd("check"); r.code != 0 {
		t.Errorf("kickd check after %s: exit %d\n%s%s", strings.Join(fixes, "; "), r.code, r.stdout, r.stderr)
	}
}

func TestServiceAgentDoesNotStartWithAConfigThatUsersCanChange(t *testing.T) {
	usersCanChange(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, kickdPath, "run")
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("kickd run started with the config:\n%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.Contains(string(out), "Config load failed") || !strings.Contains(string(out), "can be changed by") {
		t.Errorf("kickd run: exit %d\n%s", code, out)
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
	res := request(t, "POST", 9787, "/hooks/whoami", "", "Authorization", "Bearer whoami-token")
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

func TestServiceUninstallDeletesTheService(t *testing.T) {
	s := windowsService(t)
	s.mustKickd("service", "stop")
	s.mustKickd("service", "uninstall")
	waitFor(t, "sc query kickd reports no service", 60*time.Second, func() bool {
		return s.run("sc.exe", "query", "kickd").code == 1060
	}, s.describe)
}
