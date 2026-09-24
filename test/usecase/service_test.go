//go:build usecase

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
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests in this file install kickd as a service of the OS, the way the
// installation guides do, which changes the machine. They run only with
// KICKD_SERVICE_TEST=1, which the CI sets on its throwaway machines.

func requireServiceTest(t *testing.T) {
	t.Helper()
	if os.Getenv("KICKD_SERVICE_TEST") != "1" {
		t.Skip("installs kickd as a service; set KICKD_SERVICE_TEST=1 to run it")
	}
}

// kickd installed as a service of the whole system: the config at the
// usual place of the OS, and the service running as root, or as SYSTEM on
// Windows.
func TestServiceSystem(t *testing.T) {
	requireServiceTest(t)
	m := &machine{t: t, sudo: runtime.GOOS != "windows", env: os.Environ(), who: "root"}
	switch runtime.GOOS {
	case "linux":
		m.cfg, m.logFile, m.dbFile = "/etc/kickd/config.yaml", "/var/log/kickd/kickd.log", "/var/lib/kickd/kickd.db"
	case "darwin":
		m.cfg, m.logFile, m.dbFile = "/Library/Application Support/kickd/config.yaml", "/Library/Logs/kickd/kickd.log", "/Library/Application Support/kickd/kickd.db"
	case "windows":
		m.cfg, m.logFile, m.dbFile = `C:\ProgramData\kickd\config.yaml`, `C:\ProgramData\kickd\kickd.log`, `C:\ProgramData\kickd\kickd.db`
		m.who = `nt authority\system`
	}
	m.cleanup(filepath.Dir(m.cfg), filepath.Dir(m.logFile), filepath.Dir(m.dbFile))
	m.install(m.cfg, nil)
	m.check()
}

// kickd installed as a per-user service: a LaunchAgent on macOS, and a
// per-user systemd unit on Linux.
func TestServiceUser(t *testing.T) {
	requireServiceTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no per-user services")
	}
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	conf, _ := os.UserConfigDir()
	m := &machine{t: t, env: os.Environ(), flags: []string{"--user"}, who: u.Username, home: home}
	cfg := filepath.Join(conf, "kickd", "config.yaml")
	var afterInstall func()
	switch runtime.GOOS {
	case "darwin":
		m.logFile, m.dbFile = "~/Library/Logs/kickd/kickd.log", "~/Library/Application Support/kickd/kickd.db"
	case "linux":
		m.logFile, m.dbFile = "~/.local/state/kickd/kickd.log", "~/.local/state/kickd/kickd.db"
		// A per-user unit runs in the systemd of the user, which runs while
		// the user is logged in or, with lingering, from boot.
		runtimeDir := fmt.Sprintf("/run/user/%d", os.Getuid())
		m.env = append(m.env, "XDG_RUNTIME_DIR="+runtimeDir, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtimeDir+"/bus")
		m.mustSudo("loginctl", "enable-linger", u.Username)
		t.Cleanup(func() { m.runSudo("loginctl", "disable-linger", u.Username) })
		waitFor(t, "the systemd of the user", 30*time.Second, func() bool {
			_, err := os.Stat(runtimeDir + "/bus")
			return err == nil
		})
		// The steps of docs/install-linux.md after kickd service install --user.
		afterInstall = func() {
			unit := filepath.Join(home, ".config", "systemd", "user", "kickd.service")
			m.must("sed", "-i", "s/^WantedBy=multi-user.target$/WantedBy=default.target/", unit)
			m.must("systemctl", "--user", "daemon-reload")
			m.must("systemctl", "--user", "reenable", "kickd")
		}
	}
	m.cleanup(filepath.Dir(cfg), filepath.Dir(m.expand(m.logFile)), filepath.Dir(m.expand(m.dbFile)))
	m.install(cfg, afterInstall)
	m.check()
}

// machine runs the commands of a service test: with sudo for a service of
// the whole system on macOS and Linux, and as the user otherwise.
type machine struct {
	t       *testing.T
	sudo    bool
	flags   []string // the flags of every kickd service action
	env     []string
	cfg     string // the config file; empty for the default path
	logFile string // the log and the database, as kickd init writes them
	dbFile  string
	who     string // the user who runs the commands of the service
	home    string
	out     string // the file that the event of the test writes
}

// run runs a program, with sudo when the machine uses it, and returns its
// standard output, standard error and exit code.
func (m *machine) run(name string, args ...string) (string, string, int) {
	m.t.Helper()
	if m.sudo {
		return m.runSudo(name, args...)
	}
	return m.exec(name, args...)
}

func (m *machine) runSudo(name string, args ...string) (string, string, int) {
	m.t.Helper()
	return m.exec("sudo", append([]string{"-n", name}, args...)...)
}

func (m *machine) exec(name string, args ...string) (string, string, int) {
	m.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = m.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		m.t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func (m *machine) must(name string, args ...string) string {
	m.t.Helper()
	out, errOut, code := m.run(name, args...)
	if code != 0 {
		m.t.Fatalf("%s %s: exit %d\n%s%s", name, strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func (m *machine) mustSudo(name string, args ...string) string {
	m.t.Helper()
	out, errOut, code := m.runSudo(name, args...)
	if code != 0 {
		m.t.Fatalf("sudo %s %s: exit %d\n%s%s", name, strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// kickd runs a kickd subcommand, with the config of the test.
func (m *machine) kickd(args ...string) (string, string, int) {
	m.t.Helper()
	if m.cfg != "" {
		args = append(args, "-c", m.cfg)
	}
	return m.run(kickd, args...)
}

func (m *machine) mustKickd(args ...string) string {
	m.t.Helper()
	out, errOut, code := m.kickd(args...)
	if code != 0 {
		m.t.Fatalf("kickd %s: exit %d\n%s%s\n%s", strings.Join(args, " "), code, out, errOut, m.diagnose())
	}
	return out
}

// service runs a kickd service action.
func (m *machine) service(action string) string {
	m.t.Helper()
	return m.mustKickd(append([]string{"service", action}, m.flags...)...)
}

// expand replaces a leading ~ with the home directory.
func (m *machine) expand(p string) string {
	if strings.HasPrefix(p, "~") {
		return filepath.Join(m.home, p[1:])
	}
	return p
}

func (m *machine) read(path string) string {
	m.t.Helper()
	if m.sudo {
		return m.must("cat", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		m.t.Fatal(err)
	}
	return string(b)
}

func (m *machine) write(path, text string) {
	m.t.Helper()
	if !m.sudo {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			m.t.Fatal(err)
		}
		return
	}
	// cp keeps the owner and the permission of the file that kickd init
	// created.
	tmp := filepath.Join(m.t.TempDir(), "config.yaml")
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		m.t.Fatal(err)
	}
	m.must("cp", tmp, path)
}

func (m *machine) exists(path string) bool {
	m.t.Helper()
	if m.sudo {
		_, _, code := m.run("test", "-f", path)
		return code == 0
	}
	_, err := os.Stat(path)
	return err == nil
}

// cleanup stops and removes the service after the test, and removes the
// directories of its files.
func (m *machine) cleanup(dirs ...string) {
	m.t.Cleanup(func() {
		m.kickd(append([]string{"service", "stop"}, m.flags...)...)
		m.kickd(append([]string{"service", "uninstall"}, m.flags...)...)
		for _, d := range dirs {
			if filepath.Base(d) != "kickd" {
				m.t.Errorf("not removing %s, which is not a directory of kickd", d)
				continue
			}
			if m.sudo {
				m.run("rm", "-rf", d)
			} else {
				os.RemoveAll(d)
			}
		}
	})
}

// install writes the config with kickd init, replaces its events with one
// that records who runs it, and installs and starts the service.
func (m *machine) install(cfg string, afterInstall func()) {
	m.t.Helper()
	m.mustKickd("init")
	text := m.read(cfg)
	for _, p := range []string{m.logFile, m.dbFile} {
		if !strings.Contains(text, "'"+p+"'") {
			m.t.Fatalf("kickd init did not put %s in the config:\n%s", p, text)
		}
	}
	m.out = filepath.Join(m.t.TempDir(), "whoami.txt")
	command := `'id -un > "` + m.out + `"'`
	if runtime.GOOS == "windows" {
		command = `'whoami > "` + m.out + `"'`
	}
	i := strings.Index(text, "events:")
	if i < 0 {
		m.t.Fatalf("the config has no events:\n%s", text)
	}
	m.write(cfg, text[:i]+"events:\n  - name: whoami\n    command: "+command+
		"\n    triggers:\n      - type: webhook\n        path: '/hooks/whoami'\n        token: '"+token+"'\n")
	m.mustKickd("check")
	m.service("install")
	if afterInstall != nil {
		afterInstall()
	}
	m.service("start")
}

// check fires the event of the service and checks who ran it, kills the
// agent and waits for the service manager to start it again, and stops and
// removes the service.
func (m *machine) check() {
	t := m.t
	t.Helper()
	pid := m.waitAgent(0, "the service starts", 60*time.Second)
	if st := m.service("status"); !strings.Contains(st, "running") {
		t.Errorf("kickd service status: %s", st)
	}

	// kickd event runs the command as the user of the service.
	m.mustKickd("event", "whoami", "--wait", "--timeout", "60s")
	m.waitWho("kickd event")

	// A webhook request reaches the HTTP server of the service.
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8787/hooks/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook request: %v\n%s", err, m.diagnose())
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook request: status %d", res.StatusCode)
	}
	m.waitWho("a webhook request")

	// The service manager starts kickd again after it crashes.
	pid = m.waitAgent(0, "the agent runs", 60*time.Second)
	killed := time.Now()
	if runtime.GOOS == "windows" {
		m.must("taskkill", "/F", "/PID", strconv.Itoa(pid))
	} else {
		m.must("kill", "-9", strconv.Itoa(pid))
	}
	m.waitAgent(pid, "the service manager starts kickd again", 200*time.Second)
	t.Logf("the service manager started kickd again %s after it was killed", time.Since(killed).Round(time.Second))

	// The agent created its log and database where kickd init put them.
	for _, p := range []string{m.logFile, m.dbFile} {
		if !m.exists(m.expand(p)) {
			t.Errorf("%s does not exist", p)
		}
	}

	stopped := time.Now()
	m.service("stop")
	t.Logf("kickd service stop returned after %s", time.Since(stopped).Round(100*time.Millisecond))
	waitFor(t, "the service stops", 60*time.Second, func() bool { return m.agentPID() == 0 })
	t.Logf("the agent recorded its stop %s after kickd service stop", time.Since(stopped).Round(100*time.Millisecond))
	// The agent records its stop before its process ends; the next service
	// needs the address of the webhook server.
	described := false
	waitFor(t, "the agent releases the webhook address", 60*time.Second, func() bool {
		ln, err := net.Listen("tcp", "127.0.0.1:8787")
		if err == nil {
			ln.Close()
			return true
		}
		if !described && time.Since(stopped) > 3*time.Second && runtime.GOOS != "windows" {
			described = true
			lsof, _, _ := m.runSudo("lsof", "-nP", "-iTCP:8787")
			ps, _, _ := m.exec("ps", "-ax", "-o", "pid,ppid,stat,etime,command")
			var procs []string
			for _, l := range strings.Split(ps, "\n") {
				if strings.Contains(l, "kickd") {
					procs = append(procs, l)
				}
			}
			t.Logf("the webhook address is still in use %s after kickd service stop\nlsof:\n%s\nprocesses:\n%s\n%s",
				time.Since(stopped).Round(100*time.Millisecond), lsof, strings.Join(procs, "\n"), m.diagnose())
		}
		return false
	})
	t.Logf("the webhook address was free %s after kickd service stop", time.Since(stopped).Round(100*time.Millisecond))
	m.service("uninstall")
	if _, _, code := m.kickd(append([]string{"service", "status"}, m.flags...)...); code == 0 {
		t.Error("kickd service status succeeds after uninstall")
	}
}

// agentPID returns the process ID of the agent when kickd status reports it
// running, and 0 otherwise.
func (m *machine) agentPID() int {
	m.t.Helper()
	out, _, code := m.kickd("status", "--json")
	var st struct {
		Running bool
		Agent   struct{ PID int }
	}
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || !st.Running {
		return 0
	}
	return st.Agent.PID
}

// waitAgent waits until kickd status reports an agent whose process is not
// old, and returns its process ID.
func (m *machine) waitAgent(old int, what string, timeout time.Duration) int {
	m.t.Helper()
	var pid int
	waitFor(m.t, what, timeout, func() bool {
		pid = m.agentPID()
		return pid != 0 && pid != old
	}, m.diagnose)
	return pid
}

// waitWho waits until the event of the test has written the user who ran
// it, checks the user, and removes the file.
func (m *machine) waitWho(what string) {
	m.t.Helper()
	var got string
	waitFor(m.t, what+" runs the command", 60*time.Second, func() bool {
		b, err := os.ReadFile(m.out)
		got = strings.ToLower(strings.TrimSpace(string(b)))
		return err == nil && got != ""
	}, m.diagnose)
	if got != strings.ToLower(m.who) {
		m.t.Errorf("%s: the command ran as %q, want %q", what, got, m.who)
	}
	if err := os.Remove(m.out); err != nil {
		m.t.Fatal(err)
	}
}

// diagnose describes the service and the end of its log, for a failure.
func (m *machine) diagnose() string {
	status, statusErr, _ := m.kickd(append([]string{"service", "status"}, m.flags...)...)
	var manager, managerErr string
	switch runtime.GOOS {
	case "darwin":
		errLog := "/var/log/kickd.err.log"
		if m.flags != nil {
			errLog = filepath.Join(m.home, "kickd.err.log")
		}
		manager, managerErr, _ = m.run("launchctl", "list", "kickd")
		tail, _, _ := m.run("tail", "-n", "20", errLog)
		manager += "\n" + errLog + ":\n" + tail
	case "linux":
		manager, managerErr, _ = m.run("systemctl", append(m.flags, "status", "kickd", "--no-pager")...)
	case "windows":
		manager, managerErr, _ = m.run("sc", "queryex", "kickd")
	}
	status += statusErr + "\nservice manager: " + manager + managerErr
	var log string
	if runtime.GOOS != "windows" {
		log, _, _ = m.run("tail", "-n", "30", m.expand(m.logFile))
	} else if b, err := os.ReadFile(m.expand(m.logFile)); err == nil {
		lines := strings.Split(string(b), "\n")
		log = strings.Join(lines[max(0, len(lines)-30):], "\n")
	}
	return "kickd service status: " + status + "\nlog:\n" + log
}

// waitFor waits until ok reports true; on a timeout it fails the test with
// the descriptions of more.
func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool, more ...func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("%s: not after %s", what, timeout)
			for _, f := range more {
				msg += "\n" + f()
			}
			t.Fatal(msg)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
