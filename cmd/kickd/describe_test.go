package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kardianos/service"

	"github.com/etak64n/kickd/internal/config"
	"github.com/etak64n/kickd/internal/queue"
)

// The descriptions of triggers that kickd check prints.

func TestDescribeTriggerOfACronTriggerGivesTheScheduleZoneAndMissed(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerCron, Schedule: "0 3 * * *", Timezone: "Asia/Tokyo", Missed: "run"})
	if want := "cron     0 3 * * * (Asia/Tokyo, missed=run)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfACronTriggerWithoutAZoneGivesOnlyMissed(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerCron, Schedule: "*/5 * * * *", Missed: "skip"})
	if want := "cron     */5 * * * * (missed=skip)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAWebhookWithoutMethodsTakesAnyMethod(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerWebhook, Path: "/hooks/deploy", Token: "x"})
	if want := "webhook  ANY /hooks/deploy (token)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAWebhookListsItsMethodsAndChecks(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerWebhook, Path: "/hooks/build", Methods: []string{"POST", "PUT"}, Token: "x", Secret: "y", Wait: true})
	if want := "webhook  POST,PUT /hooks/build (token+secret, wait)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAWebhookWithoutTokenOrSecretSaysNoAuth(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerWebhook, Path: "/hooks/ping"})
	if want := "webhook  ANY /hooks/ping (no auth)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAFileTriggerGivesItsSettings(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerFile, Path: "/app/src", Changes: []string{"create", "write"}, Debounce: 2 * time.Second,
		Recursive: true, Include: []string{"*.c", "*.h"}, Exclude: []string{"*_test.c"}})
	if want := "file     /app/src (changes=create,write, debounce=2s, recursive, include=*.c,*.h, exclude=*_test.c)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAnAfterTriggerGivesTheEventAndTheStatuses(t *testing.T) {
	got := describeTrigger(config.Trigger{Type: config.TriggerAfter, Event: "backup", Status: []string{"failed", "abandoned"}})
	if want := "after    backup (failed, abandoned)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAStartupTrigger(t *testing.T) {
	if got, want := describeTrigger(config.Trigger{Type: config.TriggerStartup}), "startup  when the agent starts"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeTriggerOfAWakeTrigger(t *testing.T) {
	if got, want := describeTrigger(config.Trigger{Type: config.TriggerWake}), "wake     when the machine wakes from sleep"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDescribeKickShowsTheCommandThatFiresTheEvent(t *testing.T) {
	e := config.Event{Name: "deploy", Params: []config.Param{{Name: "ref", Required: true}, {Name: "target", Default: "staging"}, {Name: "note"}}}
	if got, want := describeKick(e), "manual   kickd event deploy ref=... [target=staging] [note=...]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The trigger names that kickd events prints.

func TestTriggerNamesNamesEachTriggerByItsKindAndTarget(t *testing.T) {
	e := config.Event{Triggers: []config.Trigger{
		{Type: config.TriggerCron, Schedule: "0 3 * * *"},
		{Type: config.TriggerWebhook, Path: "/hooks/deploy"},
		{Type: config.TriggerFile, Path: "/app/src"},
		{Type: config.TriggerAfter, Event: "backup"},
		{Type: config.TriggerStartup},
		{Type: config.TriggerWake},
		{Type: config.TriggerManual},
	}}
	got := strings.Join(triggerNames(e), ", ")
	if want := "cron 0 3 * * *, webhook /hooks/deploy, file /app/src, after backup, startup, wake, manual"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The run ID that kickd show and kickd cancel take.

func TestRunIDTakesANumber(t *testing.T) {
	if id, err := runID([]string{"42"}); id != 42 || err != nil {
		t.Errorf("runID(42) = %d, %v", id, err)
	}
}

func TestRunIDTakesANumberAfterAHash(t *testing.T) {
	if id, err := runID([]string{"#42"}); id != 42 || err != nil {
		t.Errorf("runID(#42) = %d, %v", id, err)
	}
}

func TestRunIDRefusesZeroANegativeNumberAndAWord(t *testing.T) {
	for _, arg := range []string{"0", "-1", "last"} {
		if _, err := runID([]string{arg}); err == nil {
			t.Errorf("runID(%s) succeeded", arg)
		}
	}
}

func TestRunIDRefusesNoIDAndTwoIDs(t *testing.T) {
	for _, args := range [][]string{nil, {"1", "2"}} {
		if _, err := runID(args); err == nil {
			t.Errorf("runID(%v) succeeded", args)
		}
	}
}

// The columns of kickd history and kickd show.

func TestStatusTextSaysThatARunningRunWasAskedToStop(t *testing.T) {
	if got := statusText(queue.Run{Status: queue.StatusRunning, CancelRequested: true}); got != "running (cancel requested)" {
		t.Errorf("got %q", got)
	}
}

func TestExitTextIsADashWithoutAnExitCode(t *testing.T) {
	code := 3
	if exitText(queue.Run{}) != "-" || exitText(queue.Run{ExitCode: &code}) != "3" {
		t.Errorf("exitText: %q, %q", exitText(queue.Run{}), exitText(queue.Run{ExitCode: &code}))
	}
}

func TestDurationTextRoundsAFinishedRunToMilliseconds(t *testing.T) {
	code := 0
	start := time.Now().Add(-time.Minute)
	r := queue.Run{Status: queue.StatusSucceeded, ExitCode: &code, StartedAt: start, FinishedAt: start.Add(1500 * time.Millisecond), Duration: 1500*time.Millisecond + 300*time.Microsecond}
	if got := durationText(r); got != "1.5s" {
		t.Errorf("got %q, want 1.5s", got)
	}
}

func TestDurationTextSaysHowLongARunningRunHasRun(t *testing.T) {
	r := queue.Run{Status: queue.StatusRunning, StartedAt: time.Now().Add(-90 * time.Second)}
	if got := durationText(r); got != "1m30s so far" {
		t.Errorf("got %q, want 1m30s so far", got)
	}
}

func TestDurationTextIsADashForARunCutOffWithoutAnExit(t *testing.T) {
	r := queue.Run{Status: queue.StatusAbandoned, StartedAt: time.Now().Add(-time.Minute), FinishedAt: time.Now()}
	if got := durationText(r); got != "-" {
		t.Errorf("got %q, want -", got)
	}
}

func TestAgoCountsSecondsMinutesAndHours(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-5 * time.Second), "5s ago"},
		{now.Add(-3 * time.Minute), "3m ago"},
		{now.Add(-5 * time.Hour), "5h ago"},
	} {
		if got := ago(c.at); got != c.want {
			t.Errorf("ago(%s) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestAgoGivesTheDateOfATimeTwoDaysAgoOrMore(t *testing.T) {
	at := time.Now().Add(-72 * time.Hour)
	if got := ago(at); got != timeText(at) {
		t.Errorf("got %q, want %q", got, timeText(at))
	}
}

func TestTimeTextIsADashForNoTime(t *testing.T) {
	if got := timeText(time.Time{}); got != "-" {
		t.Errorf("got %q", got)
	}
}

func TestReasonTextJoinsTheReasonAndTheDetail(t *testing.T) {
	for _, c := range []struct {
		r    queue.Run
		want string
	}{
		{queue.Run{Reason: "timeout", Detail: "after 10m"}, "timeout: after 10m"},
		{queue.Run{Reason: "exit_code"}, "exit_code"},
		{queue.Run{Detail: "the event was removed"}, "the event was removed"},
		{queue.Run{}, ""},
	} {
		if got := reasonText(c.r); got != c.want {
			t.Errorf("reasonText(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestFromEnvNamesAnEnvironmentVariableThatSetALogSetting(t *testing.T) {
	if fromEnv("log.level") != "" || fromEnv("LOG_LEVEL") != " (from LOG_LEVEL)" {
		t.Errorf("fromEnv: %q, %q", fromEnv("log.level"), fromEnv("LOG_LEVEL"))
	}
}

func TestStatusNameNamesTheStatusOfAService(t *testing.T) {
	for st, want := range map[service.Status]string{service.StatusRunning: "running", service.StatusStopped: "stopped", service.StatusUnknown: "unknown"} {
		if got := statusName(st); got != want {
			t.Errorf("statusName(%v) = %q, want %q", st, got, want)
		}
	}
}

// The service runs kickd run with no arguments, so the agent reads the
// config file of the user that the service runs as. It starts in the
// directory of that file.
func TestServiceConfigRunsTheAgentInTheDirectoryOfTheConfig(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "kickd", "config.yaml")
	c := serviceConfig(cfg, false)
	if c.Name != "kickd" || strings.Join(c.Arguments, " ") != "run" {
		t.Errorf("name %q, arguments %q", c.Name, c.Arguments)
	}
	if c.WorkingDirectory != filepath.Dir(cfg) {
		t.Errorf("working directory: %s", c.WorkingDirectory)
	}
}
