// Package config loads and validates the agent configuration file.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/etak64n/kickd/internal/trigger"
)

// The examples that "kickd init" writes: one with shell commands for macOS
// and Linux, and one with PowerShell commands for Windows.
var (
	//go:embed example.yaml
	exampleUnix string
	//go:embed example-windows.yaml
	exampleWindows string
)

// InitPaths holds the log file and the database that "kickd init" writes
// into a new config.
type InitPaths struct{ Log, Database string }

// initPaths are the usual places for the log and the database on each
// OS: for a user's program, and for a service of the whole system.
var initPaths = map[string]struct{ user, system InitPaths }{
	"darwin": {
		InitPaths{"~/Library/Logs/kickd/kickd.log", "~/Library/Application Support/kickd/kickd.db"},
		InitPaths{"/Library/Logs/kickd/kickd.log", "/Library/Application Support/kickd/kickd.db"},
	},
	"linux": {
		InitPaths{"~/.local/state/kickd/kickd.log", "~/.local/state/kickd/kickd.db"},
		InitPaths{"/var/log/kickd/kickd.log", "/var/lib/kickd/kickd.db"},
	},
	"windows": {
		InitPaths{`~\AppData\Local\kickd\kickd.log`, `~\AppData\Local\kickd\kickd.db`},
		InitPaths{`C:\ProgramData\kickd\kickd.log`, `C:\ProgramData\kickd\kickd.db`},
	},
}

// PathsFor returns the log file and the database that "kickd init" writes
// on the OS goos, as runtime.GOOS names it: those of a service of the whole
// system when system is true, and those of a user otherwise.
func PathsFor(goos string, system bool) InitPaths {
	p, ok := initPaths[goos]
	if !ok {
		p = initPaths["linux"]
	}
	if system {
		return p.system
	}
	return p.user
}

// Example returns the annotated configuration that "kickd init" writes on
// the OS goos: with commands for Windows or for macOS and Linux, and with
// the log and the database at the paths of PathsFor.
func Example(goos string, system bool) string {
	text := exampleUnix
	if goos == "windows" {
		text = exampleWindows
	}
	// A checkout on Windows can turn the line endings into CRLF.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	p := PathsFor(goos, system)
	return strings.NewReplacer("'LOG_PATH'", "'"+p.Log+"'", "'DATABASE_PATH'", "'"+p.Database+"'").Replace(text)
}

// Concurrency policies.
const (
	ConcurrencySkip     = "skip"
	ConcurrencyQueue    = "queue"
	ConcurrencyParallel = "parallel"
)

// What to do with a run that was cut off because kickd or the machine
// stopped while it ran.
const (
	InterruptAbandon = "abandon" // record it as abandoned
	InterruptRerun   = "rerun"   // run the same firing again
)

// Stdin modes.
const (
	StdinNone    = "none"
	StdinPayload = "payload"
)

// Trigger types. Every event can also be fired with "kickd event NAME".
const (
	TriggerCron    = "cron"
	TriggerWebhook = "webhook"
	TriggerFile    = "file"
)

// Defaults applied when a field is left empty.
const (
	DefaultListen       = "127.0.0.1:8787"
	DefaultDebounce     = time.Second
	DefaultLogLevel     = "info"
	DefaultLogFormat    = "auto"
	DefaultLogSizeMB    = 10
	DefaultLogBackups   = 5
	DefaultDatabasePath = "kickd.db"
	DefaultRetention    = 7 * 24 * time.Hour
	DefaultMaxAttempts  = 3
)

// Environment variables that override the log settings of the file, as
// the logging guideline recommends.
const (
	EnvLogLevel  = "LOG_LEVEL"
	EnvLogFormat = "LOG_FORMAT"
)

var (
	logLevels   = []string{"trace", "debug", "info", "warn", "error", "fatal"}
	logFormats  = []string{"auto", "json", "text"}
	eventNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	paramNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Config is the root of the configuration file.
type Config struct {
	Log      Log      `yaml:"log"`
	Webhook  Webhook  `yaml:"webhook"`
	Database Database `yaml:"database"`
	Events   []Event  `yaml:"events"`

	// Path is the absolute path of the loaded file and Dir its directory.
	// Relative paths in the file are resolved against Dir.
	Path string `yaml:"-"`
	Dir  string `yaml:"-"`
}

// Log configures the agent log.
type Log struct {
	Level      string `yaml:"level"`
	Format     string `yaml:"format"`
	Path       string `yaml:"path"` // empty: standard error
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`

	// LevelFrom and FormatFrom name where the value came from: the
	// config file or an environment variable.
	LevelFrom  string `yaml:"-"`
	FormatFrom string `yaml:"-"`
}

// Webhook configures the shared HTTP server used by webhook triggers.
type Webhook struct {
	Enabled      *bool  `yaml:"enabled"`
	Listen       string `yaml:"listen"`
	MaxBodyBytes int64  `yaml:"max_body_bytes"`
}

// IsEnabled reports whether webhook triggers may fire: webhook.enabled is
// true unless the file sets it to false.
func (w Webhook) IsEnabled() bool { return w.Enabled == nil || *w.Enabled }

// Database configures the SQLite database that records every run:
// waiting, running and finished.
type Database struct {
	Path      string        `yaml:"path"`
	Retention time.Duration `yaml:"retention"`
}

// Event is a named event: the command to run, what fires it, and how its
// runs are scheduled. "kickd event NAME" fires any event; triggers fire it
// automatically.
type Event struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Command     []string          `yaml:"command"`
	Shell       string            `yaml:"shell"`
	Workdir     string            `yaml:"workdir"`
	Env         map[string]string `yaml:"env"`
	Timeout     time.Duration     `yaml:"timeout"`
	Concurrency string            `yaml:"concurrency"`
	OnInterrupt string            `yaml:"on_interrupt"`
	MaxAttempts int               `yaml:"max_attempts"`
	Stdin       string            `yaml:"stdin"`
	LogOutput   *bool             `yaml:"log_output"`
	Params      []Param           `yaml:"params"`
	Triggers    []Trigger         `yaml:"triggers"`
}

// LogsOutput reports whether command output is written to the log.
func (e Event) LogsOutput() bool { return e.LogOutput == nil || *e.LogOutput }

// Param declares one KEY=VALUE argument of an event. When an event
// declares params, only those keys are accepted.
type Param struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
	Required    bool   `yaml:"required" json:"required,omitempty"`
	Default     string `yaml:"default" json:"default,omitempty"`
}

// Apply checks data against the declared params and fills in defaults.
// Without declared params any key is accepted.
func (e Event) Apply(data map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(data)+len(e.Params))
	for k, v := range data {
		out[k] = v
	}
	if len(e.Params) == 0 {
		for k := range out {
			if !paramNameRe.MatchString(k) {
				return nil, fmt.Errorf("event %q: invalid parameter name %q", e.Name, k)
			}
		}
		return out, nil
	}
	known := map[string]bool{}
	var errs []error
	for _, p := range e.Params {
		known[p.Name] = true
		if _, ok := out[p.Name]; ok {
			continue
		}
		switch {
		case p.Required:
			errs = append(errs, fmt.Errorf("event %q: parameter %s is required", e.Name, p.Name))
		case p.Default != "":
			out[p.Name] = p.Default
		}
	}
	for k := range out {
		if !known[k] {
			names := make([]string, len(e.Params))
			for i, p := range e.Params {
				names[i] = p.Name
			}
			errs = append(errs, fmt.Errorf("event %q: unknown parameter %s (declared: %s)", e.Name, k, strings.Join(names, ", ")))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Trigger fires its event automatically. Which fields apply depends on
// Type.
type Trigger struct {
	Type string `yaml:"type"`
	// Path is the watched directory (file) or the URL path (webhook).
	Path string `yaml:"path"`

	// cron
	Schedule string `yaml:"schedule"`
	Timezone string `yaml:"timezone"`
	// Missed is run or skip: what to do with scheduled times that passed
	// while the machine slept or kickd was stopped.
	Missed string `yaml:"missed"`

	// webhook
	Methods []string `yaml:"methods"`
	Token   string   `yaml:"token"`
	Secret  string   `yaml:"secret"`
	Wait    bool     `yaml:"wait"`

	// file
	Recursive bool          `yaml:"recursive"`
	Include   []string      `yaml:"include"`
	Exclude   []string      `yaml:"exclude"`
	Changes   []string      `yaml:"changes"`
	Debounce  time.Duration `yaml:"debounce"`
}

// Load reads, resolves and validates the file at path.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data, filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = abs
	return cfg, nil
}

// Parse decodes data, resolving relative paths against dir.
func Parse(data []byte, dir string) (*Config, error) {
	if err := renamedKeys(data); err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if err.Error() == "EOF" {
			return nil, errors.New("config file is empty")
		}
		return nil, err
	}
	cfg.Dir = dir
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	c.Log.LevelFrom, c.Log.FormatFrom = "log.level", "log.format"
	if v := os.Getenv(EnvLogLevel); v != "" {
		c.Log.Level, c.Log.LevelFrom = v, EnvLogLevel
	}
	if v := os.Getenv(EnvLogFormat); v != "" {
		c.Log.Format, c.Log.FormatFrom = v, EnvLogFormat
	}
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))
	if c.Log.Level == "" {
		c.Log.Level = DefaultLogLevel
	}
	if c.Log.Format == "" {
		c.Log.Format = DefaultLogFormat
	}
	if c.Log.MaxSizeMB == 0 {
		c.Log.MaxSizeMB = DefaultLogSizeMB
	}
	if c.Log.MaxBackups == 0 {
		c.Log.MaxBackups = DefaultLogBackups
	}
	c.Log.Path = c.resolve(c.Log.Path)
	if c.Webhook.Listen == "" {
		c.Webhook.Listen = DefaultListen
	}
	if c.Webhook.MaxBodyBytes == 0 {
		c.Webhook.MaxBodyBytes = trigger.DefaultMaxBody
	}
	if c.Database.Path == "" {
		c.Database.Path = DefaultDatabasePath
	}
	c.Database.Path = c.resolve(c.Database.Path)
	if c.Database.Retention == 0 {
		c.Database.Retention = DefaultRetention
	}
	for i := range c.Events {
		e := &c.Events[i]
		if e.Concurrency == "" {
			e.Concurrency = ConcurrencySkip
		}
		if e.OnInterrupt == "" {
			e.OnInterrupt = InterruptAbandon
		}
		if e.MaxAttempts == 0 {
			e.MaxAttempts = DefaultMaxAttempts
		}
		if e.Stdin == "" {
			e.Stdin = StdinNone
		}
		// Without a workdir, the command runs in the directory of the config
		// file, whether kickd runs as a service or in a terminal.
		if e.Workdir == "" {
			e.Workdir = c.Dir
		}
		e.Workdir = c.resolve(e.Workdir)
		for k := range e.Triggers {
			t := &e.Triggers[k]
			t.Type = strings.ToLower(strings.TrimSpace(t.Type))
			switch t.Type {
			case TriggerFile:
				t.Path = c.resolve(t.Path)
				if t.Debounce == 0 {
					t.Debounce = DefaultDebounce
				}
				if len(t.Changes) == 0 {
					t.Changes = slices.Clone(trigger.DefaultFileOps)
				}
			case TriggerCron:
				t.Missed = strings.ToLower(strings.TrimSpace(t.Missed))
				if t.Missed == "" {
					t.Missed = trigger.MissedRun
				}
			case TriggerWebhook:
				for m := range t.Methods {
					t.Methods[m] = strings.ToUpper(strings.TrimSpace(t.Methods[m]))
				}
			}
		}
	}
}

// resolve expands "~" and environment variables and makes p absolute,
// relative to the config directory.
// resolve expands environment variables and a leading ~ in p, and makes a
// relative result relative to the directory of the config file.
func (c *Config) resolve(p string) string {
	if p == "" {
		return ""
	}
	p = os.ExpandEnv(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.Dir, p)
	}
	return filepath.Clean(p)
}

func (c *Config) validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !slices.Contains(logLevels, c.Log.Level) {
		fail("%s %q must be one of %s", c.Log.LevelFrom, c.Log.Level, strings.Join(logLevels, ", "))
	}
	if !slices.Contains(logFormats, c.Log.Format) {
		fail("%s %q must be one of %s", c.Log.FormatFrom, c.Log.Format, strings.Join(logFormats, ", "))
	}
	if c.Log.MaxSizeMB < 0 {
		fail("log.max_size_mb must not be negative")
	}
	if c.Log.MaxBackups < 0 {
		fail("log.max_backups must not be negative")
	}
	if c.Database.Retention < 0 {
		fail("database.retention must not be negative")
	}
	if len(c.Events) == 0 {
		fail("events: at least one event is required")
	}
	names := map[string]bool{}
	hookPaths := map[string]bool{}
	for i, e := range c.Events {
		where := fmt.Sprintf("events[%d]", i)
		switch {
		case e.Name == "":
			fail("%s: name is required", where)
		case !eventNameRe.MatchString(e.Name):
			fail("%s: name %q must be 1 to 64 letters, digits, '.', '_', ':' or '-', starting with a letter or digit", where, e.Name)
		default:
			where = fmt.Sprintf("event %q", e.Name)
			if names[e.Name] {
				fail("%s: duplicate name", where)
			}
			names[e.Name] = true
		}
		switch {
		case len(e.Command) == 0 && e.Shell == "":
			fail("%s: command or shell is required", where)
		case len(e.Command) > 0 && e.Shell != "":
			fail("%s: command and shell are mutually exclusive", where)
		case len(e.Command) > 0 && e.Command[0] == "":
			fail("%s: command[0] must not be empty", where)
		}
		if !slices.Contains([]string{ConcurrencySkip, ConcurrencyQueue, ConcurrencyParallel}, e.Concurrency) {
			fail("%s: concurrency %q must be skip, queue or parallel", where, e.Concurrency)
		}
		if !slices.Contains([]string{InterruptAbandon, InterruptRerun}, e.OnInterrupt) {
			fail("%s: on_interrupt %q must be abandon or rerun", where, e.OnInterrupt)
		}
		if e.MaxAttempts < 1 {
			fail("%s: max_attempts must be at least 1", where)
		}
		if !slices.Contains([]string{StdinNone, StdinPayload}, e.Stdin) {
			fail("%s: stdin %q must be none or payload", where, e.Stdin)
		}
		if e.Timeout < 0 {
			fail("%s: timeout must not be negative", where)
		}
		if e.Workdir != "" {
			if st, err := os.Stat(e.Workdir); err != nil || !st.IsDir() {
				fail("%s: workdir %q is not a directory", where, e.Workdir)
			}
		}
		params := map[string]bool{}
		hasRequired := false
		for _, p := range e.Params {
			switch {
			case !paramNameRe.MatchString(p.Name):
				fail("%s: parameter name %q must be letters, digits and '_', not starting with a digit", where, p.Name)
			case params[p.Name]:
				fail("%s: duplicate parameter %s", where, p.Name)
			case p.Required && p.Default != "":
				fail("%s: parameter %s cannot be required and have a default", where, p.Name)
			}
			params[p.Name] = true
			hasRequired = hasRequired || p.Required
		}
		for k, t := range e.Triggers {
			twhere := fmt.Sprintf("%s triggers[%d]", where, k)
			switch t.Type {
			case TriggerCron:
				if t.Schedule == "" {
					fail("%s: schedule is required", twhere)
				} else if _, err := trigger.ParseSchedule(t.Schedule, t.Timezone); err != nil {
					fail("%s: schedule %q: %v", twhere, t.Schedule, err)
				}
				if t.Missed != trigger.MissedRun && t.Missed != trigger.MissedSkip {
					fail("%s: missed %q must be run or skip", twhere, t.Missed)
				}
				if hasRequired {
					fail("%s: a cron trigger cannot supply required parameters", twhere)
				}
				if t.Path != "" || t.Recursive || len(t.Include) > 0 || len(t.Exclude) > 0 || len(t.Changes) > 0 || t.Debounce != 0 ||
					t.Token != "" || t.Secret != "" || len(t.Methods) > 0 || t.Wait {
					fail("%s: file and webhook fields are not allowed on a cron trigger", twhere)
				}
			case TriggerWebhook:
				switch {
				case t.Path == "":
					fail("%s: path is required", twhere)
				case !strings.HasPrefix(t.Path, "/"):
					fail("%s: path %q must start with /", twhere, t.Path)
				case t.Path == "/healthz":
					fail("%s: path /healthz is reserved", twhere)
				case hookPaths[t.Path]:
					fail("%s: path %q is used by another trigger", twhere, t.Path)
				default:
					hookPaths[t.Path] = true
				}
				for _, m := range t.Methods {
					if m == "" {
						fail("%s: methods must not contain an empty entry", twhere)
					}
				}
				if t.Recursive || len(t.Include) > 0 || len(t.Exclude) > 0 || len(t.Changes) > 0 || t.Debounce != 0 ||
					t.Schedule != "" || t.Timezone != "" || t.Missed != "" {
					fail("%s: file and cron fields are not allowed on a webhook trigger", twhere)
				}
			case TriggerFile:
				if t.Path == "" {
					fail("%s: path is required", twhere)
				} else if st, err := os.Stat(t.Path); err != nil || !st.IsDir() {
					fail("%s: path %q is not a directory", twhere, t.Path)
				}
				for _, ch := range t.Changes {
					if !slices.Contains(trigger.ValidFileOps, ch) {
						fail("%s: unknown change %q (valid: %s)", twhere, ch, strings.Join(trigger.ValidFileOps, ", "))
					}
				}
				if t.Debounce < 0 {
					fail("%s: debounce must not be negative", twhere)
				}
				if hasRequired {
					fail("%s: a file trigger cannot supply required parameters", twhere)
				}
				if t.Schedule != "" || t.Timezone != "" || t.Missed != "" || t.Token != "" || t.Secret != "" || len(t.Methods) > 0 || t.Wait {
					fail("%s: cron and webhook fields are not allowed on a file trigger", twhere)
				}
			case "":
				fail("%s: type is required (cron, webhook or file)", twhere)
			default:
				fail("%s: unknown type %q (cron, webhook or file)", twhere, t.Type)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Problems: errs}
}

// ValidationError lists every problem found in a configuration.
type ValidationError struct {
	Problems []error
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.Error()
	}
	return strings.Join(msgs, "\n")
}

func (e *ValidationError) Unwrap() []error { return e.Problems }

// WebhookServer reports whether kickd runs the HTTP server of webhook
// triggers: when an event has a webhook trigger and webhook.enabled is not
// false.
func (c *Config) WebhookServer() bool { return c.HasWebhook() && c.Webhook.IsEnabled() }

// HasWebhook reports whether any event has a webhook trigger.
func (c *Config) HasWebhook() bool {
	for _, e := range c.Events {
		for _, t := range e.Triggers {
			if t.Type == TriggerWebhook {
				return true
			}
		}
	}
	return false
}

// EventByName returns the event with the given name.
func (c *Config) EventByName(name string) (Event, bool) {
	for _, e := range c.Events {
		if e.Name == name {
			return e, true
		}
	}
	return Event{}, false
}

// EventNames returns the event names in file order.
func (c *Config) EventNames() []string {
	out := make([]string, len(c.Events))
	for i, e := range c.Events {
		out[i] = e.Name
	}
	return out
}

// Warning is advice that does not block loading.
type Warning struct {
	Event  string
	Reason string // machine readable, snake_case
	Detail string
}

// Warnings returns advice that does not block loading.
func (c *Config) Warnings() []Warning {
	var out []Warning
	if !c.WebhookServer() {
		return out
	}
	host, _, err := net.SplitHostPort(c.Webhook.Listen)
	if err != nil {
		host = c.Webhook.Listen
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return out
	}
	for _, e := range c.Events {
		for _, t := range e.Triggers {
			if t.Type == TriggerWebhook && t.Token == "" && t.Secret == "" {
				out = append(out, Warning{
					Event:  e.Name,
					Reason: "webhook_without_auth",
					Detail: fmt.Sprintf("webhook %s has no token or secret while the server listens on %s", t.Path, c.Webhook.Listen),
				})
			}
		}
	}
	return out
}

// renamedKeys reports keys that earlier versions of kickd used, with the
// names that replaced them, so that an old file fails with a fix.
func renamedKeys(data []byte) error {
	var root yaml.Node
	if yaml.Unmarshal(data, &root) != nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil // the decoder reports syntax errors and empty files
	}
	var errs []error
	top := root.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		key, value := top[i], top[i+1]
		switch {
		case key.Value == "queue":
			errs = append(errs, fmt.Errorf("line %d: the queue section is now called database", key.Line))
		case key.Value == "base_dir":
			errs = append(errs, fmt.Errorf("line %d: base_dir is gone: give the full paths in log.path and database.path", key.Line))
		case key.Value == "log" && value.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(value.Content); j += 2 {
				if k := value.Content[j]; k.Value == "file" {
					errs = append(errs, fmt.Errorf("line %d: log.file is now log.path", k.Line))
				}
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Problems: errs}
}

// DefaultPath is the config file used when nothing else is specified:
// <user config dir>/kickd/config.yaml.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "kickd", "config.yaml")
}

// Resolve picks the config file: the flag value, then $KICKD_CONFIG, then
// DefaultPath if it exists, then ./kickd.yaml if it exists, else
// DefaultPath.
func Resolve(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if p := os.Getenv("KICKD_CONFIG"); p != "" {
		return p
	}
	def := DefaultPath()
	if fileExists(def) {
		return def
	}
	if fileExists("kickd.yaml") {
		return "kickd.yaml"
	}
	return def
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
