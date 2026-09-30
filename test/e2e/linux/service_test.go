//go:build e2e && linux

package linux

// The tests in this file change the machine: they install kickd as a
// systemd unit, as docs/install-linux.md does, and they move the clock.
// They run only with KICKD_MACHINE_TEST=1, which the CI sets on its
// throwaway machines. They run one at a time, because every unit listens
// on port 8787.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
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

// service is kickd installed as a systemd unit: a system-wide unit, which
// runs as root and is set up with sudo, or a per-user unit.
type service struct {
	t        *testing.T
	sudo     bool     // a system-wide unit
	flags    []string // the flags of every kickd service action
	env      []string
	config   string
	unit     string // the unit file that kickd service install writes
	log      string // where the config puts the log and the database
	database string
}

// systemUnit installs kickd as a system-wide unit with the config of
// testdata/service-system, and starts it.
func systemUnit(t *testing.T) *service {
	t.Helper()
	changesTheMachine(t)
	s := &service{
		t:        t,
		sudo:     true,
		env:      os.Environ(),
		config:   "/etc/kickd/config.yaml",
		unit:     "/etc/systemd/system/kickd.service",
		log:      "/var/log/kickd/kickd.log",
		database: "/var/lib/kickd/kickd.db",
	}
	s.removeAtEnd("/etc/kickd", "/var/log/kickd", "/var/lib/kickd")
	s.must("mkdir", "-p", "/etc/kickd")
	s.must("cp", "testdata/service-system/config.yaml", s.config)
	s.install()
	return s
}

// userUnit installs kickd as a per-user unit with the config of
// testdata/service-user, and starts it. Lingering keeps the systemd of the
// user running, as the runner of the CI does not log in. The tests leave
// it on: turned off, the systemd of the user stops while the next test
// starts it again.
func userUnit(t *testing.T) *service {
	t.Helper()
	changesTheMachine(t)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := "/run/user/" + u.Uid
	s := &service{
		t:        t,
		flags:    []string{"--user"},
		env:      append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtimeDir+"/bus"),
		config:   filepath.Join(u.HomeDir, ".config", "kickd", "config.yaml"),
		unit:     filepath.Join(u.HomeDir, ".config", "systemd", "user", "kickd.service"),
		log:      filepath.Join(u.HomeDir, ".local", "state", "kickd", "kickd.log"),
		database: filepath.Join(u.HomeDir, ".local", "state", "kickd", "kickd.db"),
	}
	s.sudoMust("loginctl", "enable-linger", u.Username)
	waitFor(t, "the systemd of the user answers", 60*time.Second, func() bool {
		return s.systemctl("show-environment").code == 0
	}, s.describe)
	s.removeAtEnd(filepath.Dir(s.config), filepath.Dir(s.log))
	s.must("mkdir", "-p", filepath.Dir(s.config))
	s.must("cp", "testdata/service-user/config.yaml", s.config)
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

// action returns the arguments of kickd service ACTION for the service.
func (s *service) action(name string) []string {
	return append([]string{"service", name}, s.flags...)
}

// removeAtEnd stops and uninstalls the service when the test ends, and
// removes the directories of its files. It also removes a unit file that a
// failed install left, which would make the next install fail.
func (s *service) removeAtEnd(dirs ...string) {
	s.t.Cleanup(func() {
		s.kickd(s.action("stop")...)
		s.kickd(s.action("uninstall")...)
		if s.unit != "" {
			s.run("rm", "-f", s.unit)
			s.systemctl("daemon-reload")
			s.systemctl("reset-failed", "kickd")
		}
		for _, d := range dirs {
			if filepath.Base(d) != "kickd" {
				s.t.Errorf("not removing %s, which is not a directory of kickd", d)
				continue
			}
			s.run("rm", "-rf", d)
		}
	})
}

// run runs a program, with sudo for a system-wide unit.
func (s *service) run(name string, args ...string) result {
	s.t.Helper()
	if s.sudo {
		return s.sudoRun(name, args...)
	}
	return s.exec(name, args...)
}

// sudoRun runs a program as root.
func (s *service) sudoRun(name string, args ...string) result {
	s.t.Helper()
	return s.exec("sudo", append([]string{"-n", name}, args...)...)
}

func (s *service) exec(name string, args ...string) result {
	s.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = s.env
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

// sudoMust runs a program as root, which has to succeed.
func (s *service) sudoMust(name string, args ...string) string {
	s.t.Helper()
	r := s.sudoRun(name, args...)
	if r.code != 0 {
		s.t.Fatalf("sudo %s %s: exit %d\n%s%s", name, strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r.stdout
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

// systemctl runs systemctl for the systemd of the service.
func (s *service) systemctl(args ...string) result {
	s.t.Helper()
	return s.run("systemctl", append(s.flags, args...)...)
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

// describe tells what systemd and kickd say about the service, for a
// failure.
func (s *service) describe() string {
	status := s.systemctl("status", "kickd", "--no-pager")
	journal := s.run("journalctl", append(s.flags, "-u", "kickd", "-n", "30", "--no-pager")...)
	log := s.run("tail", "-n", "30", s.log)
	return fmt.Sprintf("systemctl status kickd: %s%s\njournalctl -u kickd:\n%s%s\nlog:\n%s",
		status.stdout, status.stderr, journal.stdout, journal.stderr, log.stdout)
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

func TestServiceInitWritesTheSystemPathsIntoAConfigOutsideTheHome(t *testing.T) {
	changesTheMachine(t)
	s := &service{t: t, sudo: true, env: os.Environ(), config: "/etc/kickd/config.yaml"}
	s.removeAtEnd("/etc/kickd")
	s.mustKickd("init")
	text := s.must("cat", s.config)
	for _, want := range []string{"path: '/var/log/kickd/kickd.log'", "path: '/var/lib/kickd/kickd.db'"} {
		if !strings.Contains(text, want) {
			t.Errorf("the config has no %q:\n%s", want, text)
		}
	}
}

func TestServiceInitMakesTheConfigReadableOnlyByItsOwner(t *testing.T) {
	changesTheMachine(t)
	s := &service{t: t, sudo: true, env: os.Environ(), config: "/etc/kickd/config.yaml"}
	s.removeAtEnd("/etc/kickd")
	s.mustKickd("init")
	if mode := strings.TrimSpace(s.must("stat", "-c", "%a %U", s.config)); mode != "600 root" {
		t.Errorf("the config has the mode and the owner %q, want 600 root", mode)
	}
}

func TestServiceOfTheSystemRunsCommandsAsRoot(t *testing.T) {
	s := systemUnit(t)
	if r := s.waitForRun(s.fire("whoami")); r.Output != "root\n" {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceOfTheSystemAcceptsAWebhookRequest(t *testing.T) {
	s := systemUnit(t)
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
	s := systemUnit(t)
	for _, p := range []string{s.log, s.database} {
		if r := s.run("test", "-f", p); r.code != 0 {
			t.Errorf("%s does not exist", p)
		}
	}
}

func TestServiceOfTheSystemStartsAgainAfterACrash(t *testing.T) {
	s := systemUnit(t)
	pid := s.agentPID()
	killed := time.Now()
	s.must("kill", "-9", strconv.Itoa(pid))
	s.waitForAgent(pid)
	t.Logf("systemd started kickd again %s after the crash", time.Since(killed).Round(time.Second))
}

func TestServiceOfTheSystemReloadsTheConfigOnSystemctlReload(t *testing.T) {
	s := systemUnit(t)
	s.systemctl("reload", "kickd")
	waitFor(t, "the agent reloads the config", 30*time.Second, func() bool {
		return records(s.must("cat", s.log), "Config reloaded") > 0
	}, s.describe)
}

func TestServiceInstallWritesAUnitThatRestartsKickdAfter5Seconds(t *testing.T) {
	s := systemUnit(t)
	if unit := s.must("cat", s.unit); !strings.Contains(unit, "\nRestart=always\n") || !strings.Contains(unit, "\nRestartSec=5\n") {
		t.Errorf("%s:\n%s", s.unit, unit)
	}
}

func TestServiceInstallEnablesTheSystemUnitForMultiUserTarget(t *testing.T) {
	s := systemUnit(t)
	if unit := s.must("cat", s.unit); !strings.Contains(unit, "\nWantedBy=multi-user.target\n") {
		t.Errorf("%s:\n%s", s.unit, unit)
	}
	if r := s.systemctl("is-enabled", "kickd"); strings.TrimSpace(r.stdout) != "enabled" {
		t.Errorf("systemctl is-enabled kickd: %q", r.stdout)
	}
}

func TestServiceOfTheUserRunsCommandsAsTheUser(t *testing.T) {
	s := userUnit(t)
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if r := s.waitForRun(s.fire("whoami")); r.Output != u.Username+"\n" {
		t.Errorf("whoami: %s\n%s", r.Status, r.Output)
	}
}

func TestServiceOfTheUserStartsAgainAfterACrash(t *testing.T) {
	s := userUnit(t)
	pid := s.agentPID()
	killed := time.Now()
	s.must("kill", "-9", strconv.Itoa(pid))
	s.waitForAgent(pid)
	t.Logf("systemd started kickd again %s after the crash", time.Since(killed).Round(time.Second))
}

func TestServiceInstallEnablesTheUserUnitForDefaultTarget(t *testing.T) {
	s := userUnit(t)
	if unit := s.must("cat", s.unit); !strings.Contains(unit, "\nWantedBy=default.target\n") {
		t.Errorf("%s:\n%s", s.unit, unit)
	}
	if r := s.systemctl("is-enabled", "kickd"); strings.TrimSpace(r.stdout) != "enabled" {
		t.Errorf("systemctl --user is-enabled kickd: %q", r.stdout)
	}
}

func TestServiceUninstallRemovesTheUserUnit(t *testing.T) {
	s := userUnit(t)
	s.mustKickd(s.action("stop")...)
	s.mustKickd(s.action("uninstall")...)
	if _, err := os.Stat(s.unit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s after uninstall: %v", s.unit, err)
	}
}

func TestServiceStatusReportsARunningService(t *testing.T) {
	s := systemUnit(t)
	if out := s.mustKickd(s.action("status")...); strings.TrimSpace(out) != "kickd: running" {
		t.Errorf("kickd service status: %q", out)
	}
}

// systemdPath reports whether path is the PATH that systemd gives the
// commands of a unit: its default, and on Ubuntu /snap/bin after it.
func systemdPath(path string) bool {
	const def = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin"
	return path == def || path == def+":/snap/bin"
}

func TestServiceOfTheSystemRunsCommandsWithThePathOfSystemd(t *testing.T) {
	s := systemUnit(t)
	if path := environment(s.waitForRun(s.fire("show-env")))["PATH"]; !systemdPath(path) {
		t.Errorf("PATH=%s", path)
	}
}

func TestServiceOfTheSystemRunsCommandsWithoutHome(t *testing.T) {
	s := systemUnit(t)
	if home, ok := environment(s.waitForRun(s.fire("show-env")))["HOME"]; ok {
		t.Errorf("HOME=%s", home)
	}
}

func TestServiceOfTheUserRunsCommandsWithThePathOfSystemd(t *testing.T) {
	s := userUnit(t)
	if path := environment(s.waitForRun(s.fire("show-env")))["PATH"]; !systemdPath(path) {
		t.Errorf("PATH=%s", path)
	}
}

func TestServiceOfTheUserGivesCommandsTheHomeOfTheUser(t *testing.T) {
	s := userUnit(t)
	home, _ := os.UserHomeDir()
	if got := environment(s.waitForRun(s.fire("show-env")))["HOME"]; got != home {
		t.Errorf("HOME=%s, want %s", got, home)
	}
}

func TestServiceOfTheUserGivesCommandsTheRuntimeDirectoryOfTheUser(t *testing.T) {
	s := userUnit(t)
	want := "/run/user/" + strconv.Itoa(os.Getuid())
	if got := environment(s.waitForRun(s.fire("show-env")))["XDG_RUNTIME_DIR"]; got != want {
		t.Errorf("XDG_RUNTIME_DIR=%s, want %s", got, want)
	}
}
