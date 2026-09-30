package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeFiles writes each file of files, a map from the name to the text,
// into dir, and returns dir.
func writeFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// event returns an events section with one event named name.
func event(name string) string {
	return "events:\n  - name: " + name + "\n    command: 'echo " + name + "'\n    triggers:\n      - type: manual\n"
}

// names returns the names of the events of cfg, in order.
func names(cfg *Config) []string {
	var out []string
	for _, e := range cfg.Events {
		out = append(out, e.Name)
	}
	return out
}

func TestLoadAddsTheEventsOfTheYAMLFilesNextToTheConfig(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": "log:\n  level: debug\n",
		"backup.yaml": event("backup"),
		"deploy.yml":  event("deploy"),
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg); !slices.Equal(got, []string{"backup", "deploy"}) {
		t.Errorf("events %v, want backup and deploy", got)
	}
	if want := []string{filepath.Join(dir, "backup.yaml"), filepath.Join(dir, "deploy.yml")}; !slices.Equal(cfg.EventFiles, want) {
		t.Errorf("event files %v, want %v", cfg.EventFiles, want)
	}
}

func TestLoadAddsTheEventsOfTheOtherFilesInTheOrderOfTheirNames(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("first"),
		"b.yaml":      event("third"),
		"a.yaml":      event("second"),
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg); !slices.Equal(got, []string{"first", "second", "third"}) {
		t.Errorf("events %v, want first, second and third", got)
	}
}

func TestLoadSkipsAYAMLFileWithoutEvents(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"notes.yaml":  "todo:\n  - rotate the token\n",
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.EventFiles) != 0 || !slices.Equal(cfg.Skipped, []string{filepath.Join(dir, "notes.yaml")}) {
		t.Errorf("event files %v, skipped %v", cfg.EventFiles, cfg.Skipped)
	}
}

func TestLoadIgnoresFilesThatAreNotYAML(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml":     event("backup"),
		"backup.yaml.bak": event("backup"),
		"README.md":       event("readme"),
		"kickd.log":       "{}\n",
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg); !slices.Equal(got, []string{"backup"}) || len(cfg.Skipped) != 0 {
		t.Errorf("events %v, skipped %v", got, cfg.Skipped)
	}
}

func TestLoadIgnoresTheFilesOfSubdirectories(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml":        event("backup"),
		"archive/older.yaml": event("older"),
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg); !slices.Equal(got, []string{"backup"}) {
		t.Errorf("events %v, want only backup", got)
	}
}

func TestLoadRejectsANameThatTwoFilesDefine(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml":  event("backup"),
		"nightly.yaml": event("backup"),
	})
	_, err := Load(filepath.Join(dir, "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), `nightly.yaml: event "backup": duplicate name, also in config.yaml`) {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadRejectsASettingsSectionInAFileOtherThanTheConfig(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": "log:\n  level: info\n",
		"deploy.yaml": "log:\n  level: debug\n" + event("deploy"),
	})
	_, err := Load(filepath.Join(dir, "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), "deploy.yaml: line 1: log belongs in the config file") {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadNamesTheFileOfAnEventWithAnError(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"deploy.yaml": "events:\n  - name: deploy\n    triggers:\n      - type: manual\n",
	})
	_, err := Load(filepath.Join(dir, "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), `deploy.yaml: event "deploy": command is required`) {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadNamesTheFileOfAYAMLError(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"broken.yaml": "events: [\n",
	})
	_, err := Load(filepath.Join(dir, "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), "broken.yaml: ") {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadRejectsAnOldKeyInAFileOtherThanTheConfig(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"deploy.yaml": "events:\n  - name: deploy\n    shell: 'echo deploy'\n    triggers:\n      - type: manual\n",
	})
	_, err := Load(filepath.Join(dir, "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), "deploy.yaml: line 3: shell is gone") {
		t.Errorf("Load: %v", err)
	}
}

func TestAnAfterTriggerFollowsAnEventOfAnotherFile(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"notify.yaml": "events:\n  - name: notify\n    command: 'echo notify'\n    triggers:\n      - type: after\n        event: backup\n        status: [failed]\n",
	})
	if _, err := Load(filepath.Join(dir, "config.yaml")); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadTakesAnEmptyConfigWhenAnotherFileHasEvents(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": "",
		"backup.yaml": event("backup"),
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg); !slices.Equal(got, []string{"backup"}) {
		t.Errorf("events %v, want backup", got)
	}
}

func TestLoadRejectsAnEmptyConfigWithoutOtherFiles(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{"config.yaml": ""})
	if _, err := Load(filepath.Join(dir, "config.yaml")); err == nil || !strings.Contains(err.Error(), "config file is empty") {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadRejectsAConfigWhoseFilesHaveNoEvents(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{"config.yaml": "log:\n  level: info\n"})
	if _, err := Load(filepath.Join(dir, "config.yaml")); err == nil || !strings.Contains(err.Error(), "at least one event is required") {
		t.Errorf("Load: %v", err)
	}
}

func TestARelativePathOfAnotherFileStartsAtTheDirectoryOfTheConfig(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml":  "log:\n  level: info\n",
		"build.yaml":   "events:\n  - name: build\n    command: ['make']\n    workdir: 'app'\n    triggers:\n      - type: manual\n",
		"app/Makefile": "build:\n",
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Events[0].Workdir; got != filepath.Join(dir, "app") {
		t.Errorf("workdir %s, want %s", got, filepath.Join(dir, "app"))
	}
}

func TestEveryEventKnowsItsFile(t *testing.T) {
	dir := writeFiles(t, t.TempDir(), map[string]string{
		"config.yaml": event("backup"),
		"deploy.yaml": event("deploy"),
	})
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Events[0].File != filepath.Join(dir, "config.yaml") || cfg.Events[1].File != filepath.Join(dir, "deploy.yaml") {
		t.Errorf("files %s and %s", cfg.Events[0].File, cfg.Events[1].File)
	}
}

// homeDir makes dir the home directory of the test on every OS.
func homeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
}

func TestDefaultPathIsConfigYamlInTheKickdDirectoryOfTheHome(t *testing.T) {
	home := t.TempDir()
	homeDir(t, home)
	if got, want := DefaultPath(), filepath.Join(home, ".kickd", "config.yaml"); got != want {
		t.Errorf("DefaultPath() = %s, want %s", got, want)
	}
}

func TestLoadOfAMissingDefaultConfigSaysThatInitWritesOne(t *testing.T) {
	homeDir(t, t.TempDir())
	if _, err := Load(DefaultPath()); err == nil || !strings.Contains(err.Error(), "kickd init writes an example there") {
		t.Errorf("Load: %v", err)
	}
}

func TestLoadOfAMissingDefaultConfigNamesTheConfigOfAnEarlierVersion(t *testing.T) {
	homeDir(t, t.TempDir())
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	old := filepath.Join(dir, "kickd", "config.yaml")
	writeFiles(t, filepath.Dir(old), map[string]string{"config.yaml": event("backup")})
	if _, err := Load(DefaultPath()); err == nil || !strings.Contains(err.Error(), "no longer from "+old) {
		t.Errorf("Load: %v", err)
	}
}
