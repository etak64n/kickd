// Package config loads and validates the agent configuration file.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/etak64n/kickd/internal/trigger"
)

// The examples that "kickd init" writes: the settings, the same on every
// OS, and the events, with shell commands for macOS and Linux and with
// PowerShell commands for Windows.
var (
	//go:embed example-config.yaml
	exampleConfig string
	//go:embed example-events.yaml
	exampleEventsUnix string
	//go:embed example-events-windows.yaml
	exampleEventsWindows string
)

// ExampleEventsName is the name of the events file that "kickd init" writes
// next to the config file.
const ExampleEventsName = "event.example.yaml"

// InitPaths holds the log file and the database that "kickd init" writes
// into a new config.
type InitPaths struct{ Log, Database string }

// PathsFor returns the log file and the database that "kickd init" writes
// on the OS goos, as runtime.GOOS names it: the usual places for a service
// of the whole machine when system is true, and the directory .kickd of
// the home directory otherwise.
func PathsFor(goos string, system bool) InitPaths {
	switch {
	case !system && goos == "windows":
		return InitPaths{`~\.kickd\kickd.log`, `~\.kickd\kickd.db`}
	case !system:
		return InitPaths{"~/.kickd/kickd.log", "~/.kickd/kickd.db"}
	case goos == "darwin":
		return InitPaths{"/Library/Logs/kickd/kickd.log", "/Library/Application Support/kickd/kickd.db"}
	case goos == "windows":
		dir := SystemDir(goos)
		return InitPaths{dir + `\kickd.log`, dir + `\kickd.db`}
	default:
		return InitPaths{"/var/log/kickd/kickd.log", "/var/lib/kickd/kickd.db"}
	}
}

// ExampleConfig returns the annotated config file that "kickd init" writes
// on the OS goos: the log, webhook and database sections, with the log and
// the database at the paths of PathsFor.
func ExampleConfig(goos string, system bool) string {
	p := PathsFor(goos, system)
	return strings.NewReplacer("'LOG_PATH'", "'"+p.Log+"'", "'DATABASE_PATH'", "'"+p.Database+"'").Replace(lf(exampleConfig))
}

// ExampleEvents returns the annotated events file that "kickd init" writes
// next to the config file on the OS goos: with commands for Windows or for
// macOS and Linux.
func ExampleEvents(goos string) string {
	if goos == "windows" {
		return lf(exampleEventsWindows)
	}
	return lf(exampleEventsUnix)
}

// lf turns CRLF line endings, which a checkout on Windows can give the
// embedded files, into LF.
func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

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

// Trigger types. An event fires only through the triggers that it lists;
// a manual trigger lets "kickd event NAME" fire it.
const (
	TriggerManual  = "manual"
	TriggerCron    = "cron"
	TriggerWebhook = "webhook"
	TriggerFile    = "file"
	TriggerAfter   = "after"   // a run of another event ended
	TriggerStartup = "startup" // the agent started
	TriggerWake    = "wake"    // the machine woke from sleep
)

// triggerKeys are the keys, other than type, that each type of trigger
// takes.
var triggerKeys = map[string][]string{
	TriggerManual:  nil,
	TriggerCron:    {"schedule", "timezone", "missed"},
	TriggerWebhook: {"path", "methods", "token", "secret", "wait"},
	TriggerFile:    {"path", "recursive", "include", "exclude", "changes", "debounce"},
	TriggerAfter:   {"event", "status"},
	TriggerStartup: nil,
	TriggerWake:    nil,
}

// triggerTypes names the types of trigger in messages.
const triggerTypes = "manual, cron, webhook, file, after, startup or wake"

// AfterStatuses are the statuses of a run that has ended, which an after
// trigger can wait for.
var AfterStatuses = []string{"succeeded", "failed", "canceled", "skipped", "dropped", "abandoned"}

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
	// EventFiles are the other files of Dir whose events the config holds,
	// and Skipped the YAML files of Dir without an events section.
	EventFiles []string `yaml:"-"`
	Skipped    []string `yaml:"-"`
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
	Run         Command           `yaml:"command"`
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

	// Command and Shell hold Run once the file is loaded: the program and
	// the arguments of a list, or the string that the shell runs.
	Command []string `yaml:"-"`
	Shell   string   `yaml:"-"`
	// File is the absolute path of the file that defines the event, and
	// Index the position of the event in the events section of that file.
	File  string `yaml:"-"`
	Index int    `yaml:"-"`
}

// Command is what an event runs: a string, which the shell runs, or a list
// of a program and its arguments, which kickd starts directly.
type Command struct {
	Shell string
	Args  []string
}

// UnmarshalYAML reads a command given as a string or as a list.
func (c *Command) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		return nil
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str":
		c.Shell = n.Value
		return nil
	case n.Kind == yaml.SequenceNode:
		return n.Decode(&c.Args)
	}
	return fmt.Errorf("line %d: command must be a string or a list of strings", n.Line)
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

// Manual reports whether the event has a manual trigger, which lets
// "kickd event NAME" fire it.
func (e Event) Manual() bool {
	for _, t := range e.Triggers {
		if t.Type == TriggerManual {
			return true
		}
	}
	return false
}

// Apply checks data against the declared params and fills in the declared
// params left out, with their defaults or empty. Without declared params any
// key is accepted.
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
		default:
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

	// after: the event whose runs are followed, and the statuses of those
	// runs that fire.
	Event  string   `yaml:"event"`
	Status []string `yaml:"status"`
}

// setKeys returns the keys of t that have a value, other than type, as the
// config names them.
func (t Trigger) setKeys() []string {
	var keys []string
	for _, k := range []struct {
		set  bool
		name string
	}{
		{t.Path != "", "path"}, {t.Schedule != "", "schedule"}, {t.Timezone != "", "timezone"}, {t.Missed != "", "missed"},
		{len(t.Methods) > 0, "methods"}, {t.Token != "", "token"}, {t.Secret != "", "secret"}, {t.Wait, "wait"},
		{t.Recursive, "recursive"}, {len(t.Include) > 0, "include"}, {len(t.Exclude) > 0, "exclude"},
		{len(t.Changes) > 0, "changes"}, {t.Debounce != 0, "debounce"}, {t.Event != "", "event"}, {len(t.Status) > 0, "status"},
	} {
		if k.set {
			keys = append(keys, k.name)
		}
	}
	return keys
}

// Load reads, resolves and validates the config file at path, together with
// the events of every other YAML file in its directory that has an events
// section.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) && abs == Path() {
		return nil, missing(err, System())
	}
	if err != nil {
		return nil, err
	}
	others, skipped, err := eventFiles(abs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg, err := parse(data, abs, others)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Skipped = skipped
	return cfg, nil
}

// Parse decodes data, resolving relative paths against dir.
func Parse(data []byte, dir string) (*Config, error) {
	return parse(data, filepath.Join(dir, "config.yaml"), nil)
}

// eventsFile is a YAML file next to the config file that has an events
// section.
type eventsFile struct {
	path string
	data []byte
}

// eventFiles reads the YAML files, other than the config file, in the
// directory of config. It returns those with an events section, in the
// order of their names, and the paths of the others.
func eventFiles(config string) ([]eventsFile, []string, error) {
	dir := filepath.Dir(config)
	self, err := os.Stat(config)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	var files []eventsFile
	var skipped []string
	for _, e := range entries {
		name := e.Name()
		p := filepath.Join(dir, name)
		if ext := strings.ToLower(filepath.Ext(name)); ext != ".yaml" && ext != ".yml" {
			continue
		}
		// A link to a file, such as one into a repository of dotfiles,
		// counts as the file. SameFile also recognizes the config file
		// when its path was given in another case, as macOS and Windows
		// allow.
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || os.SameFile(st, self) {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		has, err := hasEvents(data)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		if !has {
			skipped = append(skipped, p)
			continue
		}
		files = append(files, eventsFile{p, data})
	}
	return files, skipped, nil
}

// hasEvents reports whether the YAML document data has a top-level events
// key.
func hasEvents(data []byte) (bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	top := root.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		if top[i].Value == "events" {
			return true, nil
		}
	}
	return false, nil
}

// decodeEvents decodes the events section of a file other than the config
// file. Such a file holds only events: the settings belong in the config
// file, where they apply to every event.
func decodeEvents(data []byte) ([]Event, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	top := root.Content[0].Content
	var errs []error
	for i := 0; i+1 < len(top); i += 2 {
		if k := top[i]; k.Value != "events" {
			errs = append(errs, fmt.Errorf("line %d: %s belongs in the config file; the other files hold only events", k.Line, k.Value))
		}
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Problems: errs}
	}
	if err := renamedKeys(data); err != nil {
		return nil, err
	}
	var f struct {
		Events []Event `yaml:"events"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return f.Events, nil
}

// parse decodes the config file at path, whose content is data, adds the
// events of the other files, and resolves and validates the result.
func parse(data []byte, path string, others []eventsFile) (*Config, error) {
	if err := renamedKeys(data); err != nil {
		return nil, err
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if !errors.Is(err, io.EOF) {
			return nil, err
		}
		if len(others) == 0 {
			return nil, errors.New("config file is empty")
		}
	}
	cfg.Path, cfg.Dir = path, filepath.Dir(path)
	for i := range cfg.Events {
		cfg.Events[i].File, cfg.Events[i].Index = path, i
	}
	for _, f := range others {
		events, err := decodeEvents(f.data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f.path), err)
		}
		for i := range events {
			events[i].File, events[i].Index = f.path, i
		}
		cfg.Events = append(cfg.Events, events...)
		cfg.EventFiles = append(cfg.EventFiles, f.path)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// missing explains a missing config file at Path, for the whole machine
// when system is true and for the user otherwise. For the user, it names
// the file of an earlier version of kickd, and the config file for the
// whole machine, when one of them is there.
func missing(err error, system bool) error {
	root, sudo := "as root", "with sudo"
	if runtime.GOOS == "windows" {
		root, sudo = "as an administrator", "in an administrator PowerShell"
	}
	if system {
		return fmt.Errorf("%w; %s, kickd reads the config file for the whole machine, and kickd init writes an example there", err, root)
	}
	user := userPath()
	if dir, derr := os.UserConfigDir(); derr == nil {
		if old := filepath.Join(dir, "kickd", "config.yaml"); fileExists(old) {
			return fmt.Errorf("%w; kickd reads its config from %s, and no longer from %s: move that file, and the files that it names, into %s",
				err, user, old, filepath.Dir(user))
		}
	}
	if machine := systemPath(); fileExists(machine) {
		return fmt.Errorf("%w; kickd init writes an example there, and %s, kickd reads %s instead", err, sudo, machine)
	}
	return fmt.Errorf("%w; kickd init writes an example there", err)
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
		if e.Run.Shell != "" || e.Run.Args != nil {
			e.Command, e.Shell = e.Run.Args, e.Run.Shell
		}
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
			case TriggerAfter:
				t.Event = strings.TrimSpace(t.Event)
				for k := range t.Status {
					t.Status[k] = strings.ToLower(strings.TrimSpace(t.Status[k]))
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
	names := map[string]string{} // the file of each event name
	hookPaths := map[string]bool{}
	defined := map[string]bool{}
	for _, e := range c.Events {
		defined[e.Name] = true
	}
	for _, e := range c.Events {
		// An event of another file than the config file is named with its
		// file, and so is its position, which counts within that file.
		file := ""
		if e.File != "" && e.File != c.Path {
			file = filepath.Base(e.File) + ": "
		}
		where := fmt.Sprintf("%sevents[%d]", file, e.Index)
		switch {
		case e.Name == "":
			fail("%s: name is required", where)
		case !eventNameRe.MatchString(e.Name):
			fail("%s: name %q must be 1 to 64 letters, digits, '.', '_', ':' or '-', starting with a letter or digit", where, e.Name)
		default:
			where = fmt.Sprintf("%sevent %q", file, e.Name)
			if other, dup := names[e.Name]; dup && other == e.File {
				fail("%s: duplicate name", where)
			} else if dup {
				fail("%s: duplicate name, also in %s", where, filepath.Base(other))
			}
			names[e.Name] = e.File
		}
		switch {
		case len(e.Command) == 0 && e.Shell == "":
			fail("%s: command is required", where)
		case len(e.Command) > 0 && e.Command[0] == "":
			fail("%s: the program of command must not be empty", where)
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
		if len(e.Triggers) == 0 {
			fail("%s: triggers is required, with at least one trigger; a manual trigger (- type: manual) lets kickd event fire the event", where)
		}
		seen := map[string]bool{}    // manual, startup and wake: at most one each
		follows := map[string]bool{} // the events that after triggers follow
		for k, t := range e.Triggers {
			twhere := fmt.Sprintf("%s triggers[%d]", where, k)
			allowed, known := triggerKeys[t.Type]
			if known {
				for _, key := range t.setKeys() {
					if !slices.Contains(allowed, key) {
						fail("%s: %s is not allowed on %s %s trigger", twhere, key, article(t.Type), t.Type)
					}
				}
			}
			switch t.Type {
			case TriggerManual, TriggerStartup, TriggerWake:
				if seen[t.Type] {
					fail("%s: the event already has a %s trigger", twhere, t.Type)
				}
				seen[t.Type] = true
				if t.Type != TriggerManual && hasRequired {
					fail("%s: a %s trigger cannot supply required parameters", twhere, t.Type)
				}
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
			case TriggerAfter:
				switch {
				case t.Event == "":
					fail("%s: event is required: the event whose runs this trigger follows", twhere)
				case t.Event == e.Name:
					fail("%s: an event cannot follow itself", twhere)
				case !defined[t.Event]:
					fail("%s: unknown event %q", twhere, t.Event)
				case follows[t.Event]:
					fail("%s: the event already follows %s", twhere, t.Event)
				}
				follows[t.Event] = true
				if len(t.Status) == 0 {
					fail("%s: status is required: the statuses of the runs that fire the event (%s)", twhere, strings.Join(AfterStatuses, ", "))
				}
				statuses := map[string]bool{}
				for _, st := range t.Status {
					switch {
					case !slices.Contains(AfterStatuses, st):
						fail("%s: unknown status %q (valid: %s)", twhere, st, strings.Join(AfterStatuses, ", "))
					case statuses[st]:
						fail("%s: duplicate status %s", twhere, st)
					}
					statuses[st] = true
				}
				if hasRequired {
					fail("%s: an after trigger cannot supply required parameters", twhere)
				}
			case "":
				fail("%s: type is required (%s)", twhere, triggerTypes)
			default:
				fail("%s: unknown type %q (%s)", twhere, t.Type, triggerTypes)
			}
		}
	}
	if cycle := afterCycle(c.Events); cycle != "" {
		fail("after triggers form a cycle, in which each run fires the next one forever: %s", cycle)
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Problems: errs}
}

// article returns the indefinite article for the type of trigger typ.
func article(typ string) string {
	if strings.HasPrefix(typ, "a") {
		return "an"
	}
	return "a"
}

// afterCycle returns a cycle of after triggers, such as "a follows b, and
// b follows a", or "" when there is none.
func afterCycle(events []Event) string {
	follows := map[string][]string{}
	for _, e := range events {
		for _, t := range e.Triggers {
			if t.Type == TriggerAfter && t.Event != "" && t.Event != e.Name {
				follows[e.Name] = append(follows[e.Name], t.Event)
			}
		}
	}
	const visiting, done = 1, 2
	state := map[string]int{}
	var path []string
	var found []string
	var visit func(string) bool
	visit = func(n string) bool {
		switch state[n] {
		case visiting:
			found = append(slices.Clone(path[slices.Index(path, n):]), n)
			return true
		case done:
			return false
		}
		state[n] = visiting
		path = append(path, n)
		for _, m := range follows[n] {
			if visit(m) {
				return true
			}
		}
		path = path[:len(path)-1]
		state[n] = done
		return false
	}
	for _, e := range events {
		if visit(e.Name) {
			break
		}
	}
	if found == nil {
		return ""
	}
	var parts []string
	for i := 0; i+1 < len(found); i++ {
		parts = append(parts, found[i]+" follows "+found[i+1])
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
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
		case key.Value == "events" && value.Kind == yaml.SequenceNode:
			for _, e := range value.Content {
				for j := 0; e.Kind == yaml.MappingNode && j+1 < len(e.Content); j += 2 {
					if k := e.Content[j]; k.Value == "shell" {
						errs = append(errs, fmt.Errorf("line %d: shell is gone: give the string as command, which runs a string through the shell", k.Line))
					}
				}
			}
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

// Path returns the config file of kickd, which the user who runs kickd
// decides: config.yaml in SystemDir when kickd runs for the whole machine,
// as System reports, and config.yaml in the directory .kickd of the home
// directory otherwise.
func Path() string {
	if System() {
		return systemPath()
	}
	return userPath()
}

// userPath is the config file of the user who runs kickd.
func userPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	abs, err := filepath.Abs(filepath.Join(home, ".kickd", "config.yaml"))
	if err != nil {
		return filepath.Join(home, ".kickd", "config.yaml")
	}
	return abs
}

// systemPath is the config file of kickd for the whole machine.
func systemPath() string {
	return filepath.Join(SystemDir(runtime.GOOS), "config.yaml")
}

// SystemDir returns the directory of the config file of kickd for the
// whole machine on the OS goos, as runtime.GOOS names it. Only root and
// administrators can change the files in it, and kickd for the whole
// machine runs the commands of its config as root or as SYSTEM.
func SystemDir(goos string) string {
	switch goos {
	case "darwin":
		return "/Library/Application Support/kickd"
	case "windows":
		return programData() + `\kickd`
	default:
		return "/etc/kickd"
	}
}

// programData is the folder in which Windows keeps the data of programs
// for all users: the value of the environment variable ProgramData, which
// is C:\ProgramData unless Windows was set up otherwise.
func programData() string {
	if dir := strings.TrimRight(os.Getenv("ProgramData"), `\`); dir != "" {
		return dir
	}
	return `C:\ProgramData`
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
