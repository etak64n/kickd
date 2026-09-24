package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func noLogEnv(t *testing.T) {
	t.Setenv(EnvLogLevel, "")
	t.Setenv(EnvLogFormat, "")
}

func TestExampleDecodes(t *testing.T) {
	// The example refers to directories that only exist on a real machine,
	// so only check that every key is known and that every OS has a base.
	for _, system := range []bool{false, true} {
		dec := yaml.NewDecoder(strings.NewReader(Example(system)))
		dec.KnownFields(true)
		var cfg Config
		if err := dec.Decode(&cfg); err != nil {
			t.Fatalf("example config does not decode: %v", err)
		}
		if len(cfg.Events) != 3 {
			t.Fatalf("events = %d, want 3", len(cfg.Events))
		}
		for _, goos := range []string{"darwin", "linux", "windows"} {
			b := cfg.BaseDir.For(goos)
			if b == "" || system == strings.HasPrefix(b, "~") {
				t.Errorf("system %v: base_dir for %s is %q", system, goos, b)
			}
		}
	}
}

// The log and the database start at the base_dir of the running OS, and
// other relative paths still start at the directory of the file.
func TestBaseDir(t *testing.T) {
	noLogEnv(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "state")
	body := "base_dir:\n  macos: '" + base + "'\n  linux: '" + base + "'\n  windows: '" + base + "'\n" +
		"log:\n  path: 'logs/kickd.log'\n" +
		"events:\n  - name: a\n    command: ['true']\n    workdir: 'work'\n"
	cfg, err := Parse([]byte(body), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "logs", "kickd.log"); cfg.Log.Path != want {
		t.Errorf("log.path = %q, want %q", cfg.Log.Path, want)
	}
	if want := filepath.Join(base, DefaultDatabasePath); cfg.Database.Path != want {
		t.Errorf("database.path = %q, want %q", cfg.Database.Path, want)
	}
	if want := filepath.Join(dir, "work"); cfg.Events[0].Workdir != want {
		t.Errorf("workdir = %q, want %q", cfg.Events[0].Workdir, want)
	}
	// An absolute path stays, and a base for another OS only does not apply.
	abs := filepath.Join(dir, "elsewhere.db")
	other := map[string]string{"darwin": "linux", "linux": "windows", "windows": "macos"}[runtime.GOOS]
	body = "base_dir:\n  " + other + ": '" + base + "'\n" +
		"database:\n  path: '" + abs + "'\n" +
		"log:\n  path: 'kickd.log'\n" +
		"events:\n  - name: a\n    command: ['true']\n"
	if cfg, err = Parse([]byte(body), dir); err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != abs || cfg.Log.Path != filepath.Join(dir, "kickd.log") || cfg.Base != dir {
		t.Errorf("database %q, log %q, base %q", cfg.Database.Path, cfg.Log.Path, cfg.Base)
	}
}

func TestLoadAppliesDefaultsAndResolvesPaths(t *testing.T) {
	noLogEnv(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "watch", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // the home directory on Windows
	t.Setenv("USERPROFILE", dir)
	path := write(t, dir, "kickd.yaml", `
log:
  path: logs/kickd.log
database:
  path: state/q.db
events:
  - name: a
    command: ["true"]
    workdir: ~/watch
    params: [{name: ref, default: main}]
    triggers:
      - type: file
        path: watch
      - type: cron
        schedule: "0 3 * * *"
        timezone: Asia/Tokyo
      - type: webhook
        path: /hooks/a
        methods: [post]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "auto" || cfg.Log.MaxSizeMB != 10 || cfg.Log.MaxBackups != 5 {
		t.Errorf("log defaults not applied: %+v", cfg.Log)
	}
	if want := filepath.Join(dir, "logs", "kickd.log"); cfg.Log.Path != want {
		t.Errorf("log.path = %q, want %q", cfg.Log.Path, want)
	}
	if want := filepath.Join(dir, "state", "q.db"); cfg.Database.Path != want || cfg.Database.Retention != DefaultRetention {
		t.Errorf("database = %+v", cfg.Database)
	}
	if !cfg.Webhook.IsEnabled() || !cfg.WebhookServer() {
		t.Errorf("webhook triggers should be enabled by default")
	}
	e := cfg.Events[0]
	if e.Concurrency != "skip" || e.OnInterrupt != "abandon" || e.MaxAttempts != 3 || e.Stdin != "none" || !e.LogsOutput() {
		t.Errorf("event defaults not applied: %+v", e)
	}
	if want := filepath.Join(dir, "watch"); e.Workdir != want {
		t.Errorf("workdir = %q, want %q (home expansion)", e.Workdir, want)
	}
	ft := e.Triggers[0]
	if want := filepath.Join(dir, "watch"); ft.Path != want || ft.Debounce != time.Second || strings.Join(ft.Changes, ",") != "create,write,remove,rename" {
		t.Errorf("file trigger = %+v", ft)
	}
	if got := e.Triggers[2].Methods; len(got) != 1 || got[0] != "POST" {
		t.Errorf("methods = %v, want [POST]", got)
	}
	if strings.Join(cfg.EventNames(), ",") != "a" || !cfg.HasWebhook() {
		t.Errorf("names = %v, webhook = %v", cfg.EventNames(), cfg.HasWebhook())
	}
}

func TestParseErrors(t *testing.T) {
	noLogEnv(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.ToSlash(filepath.Join(dir, "d"))
	const ok = "\n    command: [x]\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "empty"},
		{"no events", "log: {level: info}", "at least one event"},
		{"missing name", "events:\n  - command: [x]", "name is required"},
		{"bad name", "events:\n  - name: 'has space'" + ok, "must be 1 to 64"},
		{"duplicate name", "events:\n  - name: a" + ok + "  - name: a" + ok, "duplicate name"},
		{"no command", "events:\n  - name: a\n", "command or shell is required"},
		{"both command and shell", "events:\n  - name: a\n    shell: x\n    command: [x]\n", "mutually exclusive"},
		{"bad concurrency", "events:\n  - name: a\n    concurrency: sometimes" + ok, "concurrency"},
		{"bad on_interrupt", "events:\n  - name: a\n    on_interrupt: resume" + ok, "on_interrupt"},
		{"bad max_attempts", "events:\n  - name: a\n    max_attempts: -1" + ok, "max_attempts"},
		{"bad stdin", "events:\n  - name: a\n    stdin: event" + ok, "stdin"},
		{"unknown trigger type", "events:\n  - name: a" + ok + "    triggers: [{type: timer}]", "unknown type"},
		{"bad cron", "events:\n  - name: a" + ok + "    triggers: [{type: cron, schedule: 'every day'}]", "schedule"},
		{"bad timezone", "events:\n  - name: a" + ok + "    triggers: [{type: cron, schedule: '@hourly', timezone: Mars/Olympus}]", "timezone"},
		{"webhook path without slash", "events:\n  - name: a" + ok + "    triggers: [{type: webhook, path: hooks}]", "must start with /"},
		{"webhook reserved path", "events:\n  - name: a" + ok + "    triggers: [{type: webhook, path: /healthz}]", "reserved"},
		{"duplicate webhook path", "events:\n  - name: a" + ok + "    triggers: [{type: webhook, path: /h}]\n  - name: b" + ok + "    triggers: [{type: webhook, path: /h}]", "used by another"},
		{"missing dir", "events:\n  - name: a" + ok + "    triggers: [{type: file, path: " + d + "/nope}]", "not a directory"},
		{"bad change", "events:\n  - name: a" + ok + "    triggers: [{type: file, path: " + d + ", changes: [touch]}]", "unknown change"},
		{"old file key", "events:\n  - name: a" + ok + "    triggers: [{type: file, path: " + d + ", events: [create]}]", "events"},
		{"unknown field", "events:\n  - name: a\n    comand: [x]", "comand"},
		{"jobs are gone", "jobs:\n  - name: a" + ok, "jobs"},
		{"numeric timeout", "events:\n  - name: a\n    timeout: 30" + ok, "time.Duration"},
		{"field of another trigger type", "events:\n  - name: a" + ok + "    triggers: [{type: cron, schedule: '@hourly', token: x}]", "not allowed"},
		{"bad param", "events:\n  - name: a\n    params: [{name: 1x}]" + ok, "parameter name"},
		{"required with default", "events:\n  - name: a\n    params: [{name: p, required: true, default: d}]" + ok, "cannot be required and have a default"},
		{"required param with cron", "events:\n  - name: a\n    params: [{name: p, required: true}]" + ok + "    triggers: [{type: cron, schedule: '@hourly'}]", "cannot supply required parameters"},
		{"negative retention", "database: {retention: -1h}\nevents:\n  - name: a" + ok, "retention"},
		{"old log key", "log:\n  file: kickd.log\nevents:\n  - name: a" + ok, "line 2: log.file is now log.path"},
		{"old queue section", "queue:\n  path: kickd.db\nevents:\n  - name: a" + ok, "line 1: the queue section is now called database"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.body), dir)
			if err == nil {
				t.Fatalf("expected an error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestParamsApply(t *testing.T) {
	noLogEnv(t)
	cfg, err := Parse([]byte(`
events:
  - name: deploy
    command: [x]
    params:
      - {name: ref, default: main}
      - {name: env, required: true}
    triggers: [{type: webhook, path: /d}]
  - name: free
    command: [x]
`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e, _ := cfg.EventByName("deploy")
	if got, err := e.Apply(map[string]string{"env": "prod"}); err != nil || got["ref"] != "main" || got["env"] != "prod" {
		t.Errorf("defaults = %v, %v", got, err)
	}
	if _, err := e.Apply(map[string]string{"ref": "x"}); err == nil || !strings.Contains(err.Error(), "env is required") {
		t.Errorf("missing required: %v", err)
	}
	if _, err := e.Apply(map[string]string{"env": "p", "colour": "red"}); err == nil || !strings.Contains(err.Error(), "unknown parameter colour") {
		t.Errorf("unknown key: %v", err)
	}
	free, _ := cfg.EventByName("free")
	if got, err := free.Apply(map[string]string{"anything": "1"}); err != nil || got["anything"] != "1" {
		t.Errorf("free-form = %v, %v", got, err)
	}
	if _, err := free.Apply(map[string]string{"bad-key": "1"}); err == nil {
		t.Error("keys must be usable as environment variable names")
	}
	if _, ok := cfg.EventByName("nope"); ok {
		t.Error("unknown event found")
	}
}

func TestWarnings(t *testing.T) {
	noLogEnv(t)
	cfg, err := Parse([]byte(`
webhook:
  listen: "0.0.0.0:9000"
events:
  - name: open
    command: [x]
    triggers: [{type: webhook, path: /open}]
  - name: closed
    command: [x]
    triggers: [{type: webhook, path: /closed, token: t}]
`), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Warnings()
	if len(w) != 1 || w[0].Event != "open" || w[0].Reason != "webhook_without_auth" || !strings.Contains(w[0].Detail, "/open") {
		t.Fatalf("warnings = %+v", w)
	}
	cfg.Webhook.Listen = "127.0.0.1:9000"
	if w := cfg.Warnings(); len(w) != 0 {
		t.Fatalf("loopback should not warn: %v", w)
	}
	cfg.Webhook.Listen = "0.0.0.0:9000"
	off := false
	cfg.Webhook.Enabled = &off
	if w := cfg.Warnings(); len(w) != 0 || cfg.WebhookServer() {
		t.Fatalf("a disabled server should not warn or run: %v", w)
	}
}

func TestLogEnvironmentOverrides(t *testing.T) {
	body := []byte("log: {level: warn, format: json}\nevents:\n  - name: a\n    command: [x]\n")
	t.Setenv(EnvLogLevel, "DEBUG")
	t.Setenv(EnvLogFormat, " Text ")
	cfg, err := Parse(body, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" || cfg.Log.Format != "text" || cfg.Log.LevelFrom != EnvLogLevel || cfg.Log.FormatFrom != EnvLogFormat {
		t.Fatalf("log = %+v", cfg.Log)
	}
	t.Setenv(EnvLogLevel, "verbose")
	_, err = Parse(body, t.TempDir())
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), `LOG_LEVEL "verbose"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("KICKD_CONFIG", "")
	if got := Resolve("x.yaml"); got != "x.yaml" {
		t.Errorf("flag = %q", got)
	}
	t.Setenv("KICKD_CONFIG", "/etc/kickd/config.yaml")
	if got := Resolve(""); got != "/etc/kickd/config.yaml" {
		t.Errorf("env = %q", got)
	}
}

func TestCronMissed(t *testing.T) {
	dir := t.TempDir()
	load := func(missed string) (*Config, error) {
		body := "events:\n  - name: a\n    shell: x\n    triggers:\n      - type: cron\n        schedule: \"0 3 * * *\"\n"
		if missed != "" {
			body += "        missed: " + missed + "\n"
		}
		path := filepath.Join(dir, "kickd.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	for missed, want := range map[string]string{"": "run", "skip": "skip", "SKIP": "skip", "run": "run"} {
		cfg, err := load(missed)
		if err != nil {
			t.Fatalf("missed %q: %v", missed, err)
		}
		if got := cfg.Events[0].Triggers[0].Missed; got != want {
			t.Errorf("missed %q: got %q, want %q", missed, got, want)
		}
	}
	if _, err := load("later"); err == nil || !strings.Contains(err.Error(), "missed \"later\" must be run or skip") {
		t.Errorf("an unknown value must fail: %v", err)
	}
	path := filepath.Join(dir, "hook.yaml")
	body := "events:\n  - name: a\n    shell: x\n    triggers:\n      - {type: webhook, path: /h, missed: run}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not allowed on a webhook trigger") {
		t.Errorf("missed on a webhook trigger must fail: %v", err)
	}
}
