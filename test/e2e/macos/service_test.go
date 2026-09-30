//go:build e2e && darwin

package macos

// The tests in this file change the Mac: they install kickd as a launchd
// service, as docs/install-macos.md does, and they move the clock. They run
// only with KICKD_MACHINE_TEST=1, which the CI sets on its throwaway
// machines. They run one at a time, because every service listens on port
// 8787.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// changesTheMac skips a test that changes the Mac, unless
// KICKD_MACHINE_TEST is 1.
func changesTheMac(t *testing.T) {
	t.Helper()
	if os.Getenv("KICKD_MACHINE_TEST") != "1" {
		t.Skip("changes the Mac; set KICKD_MACHINE_TEST=1 to run it")
	}
}

// service is kickd installed as a launchd service: a LaunchDaemon, which
// runs as root and is set up with sudo, or a LaunchAgent of the user.
type service struct {
	t        *testing.T
	sudo     bool // a LaunchDaemon
	config   string
	log      string // where the config puts the log and the database
	database string
}

// launchDaemon installs kickd as a LaunchDaemon with the config of
// testdata/service-daemon, and starts it.
func launchDaemon(t *testing.T) *service {
	t.Helper()
	changesTheMac(t)
	s := &service{
		t:        t,
		sudo:     true,
		config:   "/Library/Application Support/kickd/config.yaml",
		log:      "/Library/Logs/kickd/kickd.log",
		database: "/Library/Application Support/kickd/kickd.db",
	}
	s.removeAtEnd(filepath.Dir(s.config), filepath.Dir(s.log))
	s.must("mkdir", "-p", filepath.Dir(s.config))
	s.must("cp", "testdata/service-daemon/config.yaml", s.config)
	s.install()
	return s
}

// launchAgent installs kickd as a LaunchAgent of the user with the config of
// testdata/service-agent, and starts it.
func launchAgent(t *testing.T) *service {
	t.Helper()
	changesTheMac(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s := &service{
		t:        t,
		config:   filepath.Join(home, ".kickd", "config.yaml"),
		log:      filepath.Join(home, ".kickd", "kickd.log"),
		database: filepath.Join(home, ".kickd", "kickd.db"),
	}
	s.removeAtEnd(filepath.Dir(s.config))
	s.must("mkdir", "-p", filepath.Dir(s.config))
	s.must("cp", "testdata/service-agent/config.yaml", s.config)
	s.install()
	return s
}

// install installs and starts the service, and waits until its agent runs.
func (s *service) install() {
	s.t.Helper()
	s.mustKickd(s.action("install")...)
	s.mustKickd(s.action("start")...)
	s.waitForAgent(0)
}

// action returns the arguments of kickd service ACTION. kickd works on the
// LaunchDaemon with sudo, and on the LaunchAgent without it.
func (s *service) action(name string) []string {
	return []string{"service", name}
}

// removeAtEnd stops and uninstalls the service when the test ends, and
// removes the directories of its files.
func (s *service) removeAtEnd(dirs ...string) {
	s.t.Cleanup(func() {
		s.kickd(s.action("stop")...)
		s.kickd(s.action("uninstall")...)
		for _, d := range dirs {
			if b := filepath.Base(d); b != "kickd" && b != ".kickd" {
				s.t.Errorf("not removing %s, which is not a directory of kickd", d)
				continue
			}
			s.run("rm", "-rf", d)
		}
		// The next test starts a service on the same address. A connection
		// of root stays in TIME_WAIT for up to 30 seconds after the service
		// stops, and until then macOS lets no other user bind its address.
		waitFor(s.t, "port 8787 is free", 60*time.Second, func() bool {
			ln, err := net.Listen("tcp", "127.0.0.1:8787")
			if err == nil {
				ln.Close()
			}
			return err == nil
		}, s.describe)
	})
}

// run runs a program, with sudo for a LaunchDaemon.
func (s *service) run(name string, args ...string) result {
	s.t.Helper()
	if s.sudo {
		name, args = "sudo", append([]string{"-n", name}, args...)
	}
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

// must runs a program that has to succeed, and returns its output.
func (s *service) must(name string, args ...string) string {
	s.t.Helper()
	r := s.run(name, args...)
	if r.code != 0 {
		s.t.Fatalf("%s %s: exit %d\n%s%s", name, strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r.stdout
}

// kickd runs kickd with args, with sudo for the LaunchDaemon, so that it
// reads the config of the service.
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

// describe tells what launchd and kickd say about the service, for a
// failure.
func (s *service) describe() string {
	status := s.kickd(s.action("status")...)
	launchd := s.run("launchctl", "list", "kickd")
	errLog := "/var/log/kickd.err.log"
	if !s.sudo {
		home, _ := os.UserHomeDir()
		errLog = filepath.Join(home, "kickd.err.log")
	}
	stderr := s.run("tail", "-n", "20", errLog)
	log := s.run("tail", "-n", "30", s.log)
	return fmt.Sprintf("kickd service status: %s%s\nlaunchctl list kickd: %s%s\n%s:\n%s\nlog:\n%s",
		status.stdout, status.stderr, launchd.stdout, launchd.stderr, errLog, stderr.stdout, log.stdout)
}

// environment returns the variables that the command of the run printed,
// one NAME=value on each line.
func environment(r run) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(r.Output, "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestServiceInitWithSudoWritesTheConfigForTheWholeMachine(t *testing.T) {
	changesTheMac(t)
	s := &service{t: t, sudo: true, config: "/Library/Application Support/kickd/config.yaml"}
	s.removeAtEnd(filepath.Dir(s.config))
	if out := s.mustKickd("init"); !strings.HasPrefix(out, "wrote "+s.config+"\n") {
		t.Errorf("sudo kickd init:\n%s", out)
	}
	text := s.must("cat", s.config)
	for _, want := range []string{"path: '/Library/Logs/kickd/kickd.log'", "path: '/Library/Application Support/kickd/kickd.db'"} {
		if !strings.Contains(text, want) {
			t.Errorf("the config has no %q:\n%s", want, text)
		}
	}
}

func TestServiceCheckWithSudoReadsTheConfigForTheWholeMachine(t *testing.T) {
	changesTheMac(t)
	s := &service{t: t, sudo: true, config: "/Library/Application Support/kickd/config.yaml"}
	s.removeAtEnd(filepath.Dir(s.config))
	s.must("mkdir", "-p", filepath.Dir(s.config))
	s.must("cp", "testdata/service-daemon/config.yaml", s.config)
	if out := s.mustKickd("check"); !strings.HasPrefix(out, "OK: "+s.config+" ") {
		t.Errorf("sudo kickd check:\n%s", out)
	}
}

func TestServiceOfTheSystemRunsCommandsAsRoot(t *testing.T) {
	s := launchDaemon(t)
	if r := s.waitForRun(s.fire("whoami")); r.Output != "root\n" {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceOfTheSystemAcceptsAWebhookRequest(t *testing.T) {
	s := launchDaemon(t)
	res := request(t, "POST", 8787, "/hooks/whoami", "", "Authorization", "Bearer whoami-token")
	var accepted struct{ RequestID string }
	if res.status != 202 || json.Unmarshal([]byte(res.body), &accepted) != nil {
		t.Fatalf("POST /hooks/whoami: %d %s", res.status, res.body)
	}
	if r := s.waitForRequest("whoami", accepted.RequestID); r.Trigger != "webhook" || r.Output != "root\n" {
		t.Errorf("whoami: %s by %s\n%s", r.Status, r.Trigger, r.Output)
	}
}

func TestServiceOfTheSystemWritesTheLogAndTheDatabaseWhereTheConfigSays(t *testing.T) {
	s := launchDaemon(t)
	for _, p := range []string{s.log, s.database} {
		if r := s.run("test", "-f", p); r.code != 0 {
			t.Errorf("%s does not exist", p)
		}
	}
}

func TestServiceOfTheSystemStartsAgainAfterACrash(t *testing.T) {
	s := launchDaemon(t)
	pid := s.agentPID()
	killed := time.Now()
	s.must("kill", "-9", strconv.Itoa(pid))
	s.waitForAgent(pid)
	t.Logf("launchd started kickd again %s after the crash", time.Since(killed).Round(time.Second))
}

func TestServiceOfTheUserRunsCommandsAsTheUser(t *testing.T) {
	s := launchAgent(t)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if r := s.waitForRun(s.fire("whoami")); r.Output != u.Username+"\n" {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceOfTheUserStartsAgainAfterACrash(t *testing.T) {
	s := launchAgent(t)
	pid := s.agentPID()
	killed := time.Now()
	s.must("kill", "-9", strconv.Itoa(pid))
	s.waitForAgent(pid)
	t.Logf("launchd started kickd again %s after the crash", time.Since(killed).Round(time.Second))
}

func TestServiceInstallWritesALaunchAgentThatRunsKickdInTheKickdDirectory(t *testing.T) {
	s := launchAgent(t)
	home, _ := os.UserHomeDir()
	plist := s.must("cat", filepath.Join(home, "Library", "LaunchAgents", "kickd.plist"))
	for _, want := range []string{"<string>" + kickdPath + "</string>", "<string>run</string>", "<string>" + filepath.Dir(s.config) + "</string>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("kickd.plist has no %s:\n%s", want, plist)
		}
	}
}

func TestServiceUninstallRemovesTheLaunchAgent(t *testing.T) {
	s := launchAgent(t)
	s.mustKickd(s.action("uninstall")...)
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "kickd.plist")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("kickd.plist after uninstall: %v", err)
	}
	waitFor(t, "the agent stops", 60*time.Second, func() bool { return s.agentPID() == 0 }, s.describe)
}

func TestServiceStatusReportsARunningService(t *testing.T) {
	s := launchAgent(t)
	if out := s.mustKickd(s.action("status")...); strings.TrimSpace(out) != "kickd: running" {
		t.Errorf("kickd service status: %q", out)
	}
}

func TestServiceOfTheUserRunsCommandsWithThePathOfLaunchd(t *testing.T) {
	s := launchAgent(t)
	if path := environment(s.waitForRun(s.fire("show-env")))["PATH"]; path != "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Errorf("PATH=%s", path)
	}
}

func TestServiceOfTheUserGivesCommandsTheHomeOfTheUser(t *testing.T) {
	s := launchAgent(t)
	home, _ := os.UserHomeDir()
	if got := environment(s.waitForRun(s.fire("show-env")))["HOME"]; got != home {
		t.Errorf("HOME=%s, want %s", got, home)
	}
}

func TestServiceOfTheUserRunsCommandsWithoutLang(t *testing.T) {
	s := launchAgent(t)
	if lang, ok := environment(s.waitForRun(s.fire("show-env")))["LANG"]; ok {
		t.Errorf("LANG=%s", lang)
	}
}
