package event

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// names returns the names of the variables in env, and their values.
func names(env []string) ([]string, map[string]string) {
	var out []string
	values := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out = append(out, k)
		values[k] = v
	}
	return out, values
}

// Every firing gets the same variables, whatever its trigger, and those of
// another kind of trigger are empty.
func TestEnvHasTheSameNamesForEveryTrigger(t *testing.T) {
	at := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	data := map[string]string{"ref": "main"}
	firings := map[string]Event{
		KindManual:  {Name: "e", Trigger: KindManual, TriggerID: "manual", Time: at, Data: data, Source: "alice@laptop"},
		KindCron:    {Name: "e", Trigger: KindCron, TriggerID: "cron:0 3 * * *", Time: at, Data: data, Cron: &CronInfo{Schedule: "0 3 * * *", ScheduledAt: at}},
		KindWebhook: {Name: "e", Trigger: KindWebhook, TriggerID: "webhook:/h", Time: at, Data: data, Webhook: &WebhookInfo{Method: "POST", Path: "/h", RemoteAddr: "127.0.0.1:1"}},
		KindFile:    {Name: "e", Trigger: KindFile, TriggerID: "file:/w", Time: at, Data: data, Files: []FileChange{{Path: "/w/a", Op: "create"}}},
		KindAfter:   {Name: "e", Trigger: KindAfter, TriggerID: "after:b", Time: at, Data: data, After: &AfterInfo{Event: "b", RunID: 7, Status: "failed", ExitCode: new(int)}},
		KindStartup: {Name: "e", Trigger: KindStartup, TriggerID: "startup", Time: at, Data: data},
		KindWake:    {Name: "e", Trigger: KindWake, TriggerID: "wake", Time: at, Data: data, Wake: &WakeInfo{SleptAt: at.Add(-time.Hour), SleptSeconds: 3600}},
	}
	var first []string
	for kind, ev := range firings {
		got, values := names(ev.Env())
		if first == nil {
			first = got
		} else if !slices.Equal(got, first) {
			t.Errorf("%s: names %v, want %v", kind, got, first)
		}
		for name, v := range values {
			other := strings.HasPrefix(name, "KICKD_MANUAL_") || strings.HasPrefix(name, "KICKD_CRON_") ||
				strings.HasPrefix(name, "KICKD_WEBHOOK_") || strings.HasPrefix(name, "KICKD_FILE_") ||
				strings.HasPrefix(name, "KICKD_AFTER_") || strings.HasPrefix(name, "KICKD_WAKE_")
			own := strings.HasPrefix(name, "KICKD_"+strings.ToUpper(kind)+"_")
			if other && !own && v != "" {
				t.Errorf("%s: %s = %q, want empty", kind, name, v)
			}
		}
		if values["KICKD_DATA"] != `{"ref":"main"}` || values["KICKD_DATA_REF"] != "main" {
			t.Errorf("%s: KICKD_DATA %q, KICKD_DATA_REF %q", kind, values["KICKD_DATA"], values["KICKD_DATA_REF"])
		}
	}
	_, after := names(firings[KindAfter].Env())
	_, wake := names(firings[KindWake].Env())
	if after["KICKD_AFTER_EVENT"] != "b" || after["KICKD_AFTER_RUN_ID"] != "7" || after["KICKD_AFTER_STATUS"] != "failed" || after["KICKD_AFTER_EXIT_CODE"] != "0" {
		t.Errorf("after variables: %v", after)
	}
	if wake["KICKD_WAKE_SLEPT_AT"] != "2026-09-25T02:00:00Z" || wake["KICKD_WAKE_SLEPT_SECONDS"] != "3600" {
		t.Errorf("wake variables: %v", wake)
	}
	for _, old := range []string{"KICKD_EVENT_DATA", "KICKD_SOURCE"} {
		if slices.Contains(first, old) {
			t.Errorf("%s is still set", old)
		}
	}
}

// env returns the value of each variable in the environment of ev.
func env(ev Event) map[string]string {
	_, values := names(ev.Env())
	return values
}

func TestEnvGivesTheScheduleAndTheScheduledTimeOfACronFiring(t *testing.T) {
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))
	v := env(Event{Trigger: KindCron, Cron: &CronInfo{Schedule: "0 12 * * *", ScheduledAt: at}})
	if v["KICKD_CRON_SCHEDULE"] != "0 12 * * *" || v["KICKD_CRON_SCHEDULED_AT"] != "2026-09-25T03:00:00Z" || v["KICKD_CRON_MISSED"] != "0" {
		t.Errorf("cron variables: %v", v)
	}
}

func TestEnvMarksACronTimeThatWasMissed(t *testing.T) {
	v := env(Event{Trigger: KindCron, Cron: &CronInfo{Schedule: "0 3 * * *", Missed: true}})
	if v["KICKD_CRON_MISSED"] != "1" {
		t.Errorf("KICKD_CRON_MISSED = %q, want 1", v["KICKD_CRON_MISSED"])
	}
}

func TestEnvGivesTheLastFileChangeAndAllThePaths(t *testing.T) {
	v := env(Event{Trigger: KindFile, Files: []FileChange{{Path: "/src/a.c", Op: "create"}, {Path: "/src/b.c", Op: "write"}}})
	sep := string(os.PathListSeparator)
	if v["KICKD_FILE_PATH"] != "/src/b.c" || v["KICKD_FILE_OP"] != "write" || v["KICKD_FILE_COUNT"] != "2" || v["KICKD_FILE_PATHS"] != "/src/a.c"+sep+"/src/b.c" {
		t.Errorf("file variables: %v", v)
	}
}

func TestEnvGivesTheRequestOfAWebhookFiring(t *testing.T) {
	v := env(Event{Trigger: KindWebhook, Webhook: &WebhookInfo{Method: "POST", Path: "/hooks/deploy", RemoteAddr: "127.0.0.1:50000"}})
	if v["KICKD_WEBHOOK_METHOD"] != "POST" || v["KICKD_WEBHOOK_PATH"] != "/hooks/deploy" || v["KICKD_WEBHOOK_REMOTE_ADDR"] != "127.0.0.1:50000" {
		t.Errorf("webhook variables: %v", v)
	}
}

func TestEnvGivesTheSourceOfAManualFiring(t *testing.T) {
	if v := env(Event{Trigger: KindManual, Source: "alice@laptop"}); v["KICKD_MANUAL_SOURCE"] != "alice@laptop" {
		t.Errorf("KICKD_MANUAL_SOURCE = %q", v["KICKD_MANUAL_SOURCE"])
	}
}

func TestEnvLeavesTheExitCodeOfAnAfterFiringEmptyWhenTheRunHadNone(t *testing.T) {
	v := env(Event{Trigger: KindAfter, After: &AfterInfo{Event: "backup", RunID: 3, Status: "abandoned"}})
	if v["KICKD_AFTER_STATUS"] != "abandoned" || v["KICKD_AFTER_EXIT_CODE"] != "" {
		t.Errorf("after variables: %v", v)
	}
}

func TestEnvCountsAFiringWithoutAnAttemptAsTheFirstAttempt(t *testing.T) {
	if v := env(Event{}); v["KICKD_ATTEMPT"] != "1" {
		t.Errorf("KICKD_ATTEMPT = %q, want 1", v["KICKD_ATTEMPT"])
	}
}

func TestEnvGivesTheAttemptOfARerun(t *testing.T) {
	if v := env(Event{Attempt: 2}); v["KICKD_ATTEMPT"] != "2" {
		t.Errorf("KICKD_ATTEMPT = %q, want 2", v["KICKD_ATTEMPT"])
	}
}

func TestEnvLeavesTheRunIDEmptyBeforeTheRunHasOne(t *testing.T) {
	if v := env(Event{}); v["KICKD_RUN_ID"] != "" {
		t.Errorf("KICKD_RUN_ID = %q, want empty", v["KICKD_RUN_ID"])
	}
	if v := env(Event{RunID: 42}); v["KICKD_RUN_ID"] != "42" {
		t.Errorf("KICKD_RUN_ID = %q, want 42", v["KICKD_RUN_ID"])
	}
}

func TestEnvGivesAnEmptyObjectForAFiringWithoutParameters(t *testing.T) {
	if v := env(Event{}); v["KICKD_DATA"] != "{}" {
		t.Errorf("KICKD_DATA = %q, want {}", v["KICKD_DATA"])
	}
}

func TestEnvNamesEachParameterInCapitals(t *testing.T) {
	v := env(Event{Data: map[string]string{"ref": "v1.2", "Target": "staging"}})
	if v["KICKD_DATA_REF"] != "v1.2" || v["KICKD_DATA_TARGET"] != "staging" {
		t.Errorf("parameter variables: %v", v)
	}
}

func TestEnvWritesTheTimeOfTheFiringInUTC(t *testing.T) {
	at := time.Date(2026, 9, 25, 9, 30, 0, 0, time.FixedZone("JST", 9*3600))
	if v := env(Event{Time: at}); v["KICKD_TIME"] != "2026-09-25T00:30:00Z" {
		t.Errorf("KICKD_TIME = %q", v["KICKD_TIME"])
	}
}

func TestThePayloadHasEveryKeyForEveryTrigger(t *testing.T) {
	b, err := json.Marshal(Event{Name: "backup", Trigger: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"requestId", "runId", "attempt", "event", "trigger", "triggerId", "time", "data", "source", "files", "cron", "webhook", "after", "wake"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("the payload has no %s: %s", key, b)
		}
	}
}

func TestNewIDIsSixteenHexDigits(t *testing.T) {
	id := NewID()
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Errorf("NewID() = %q", id)
	}
}

func TestNewIDDiffersForEachFiring(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if seen[id] {
			t.Fatalf("NewID() returned %s twice", id)
		}
		seen[id] = true
	}
}

func TestHandlerFuncDispatchHandsTheFiringOverAndQueuesIt(t *testing.T) {
	var got Event
	st := HandlerFunc(func(ev Event) { got = ev }).Dispatch(Event{Name: "backup"})
	if got.Name != "backup" || st != Queued {
		t.Errorf("got %q, status %s", got.Name, st)
	}
}

func TestHandlerFuncRunSyncHandsTheFiringOverWithAnEmptyResult(t *testing.T) {
	var got Event
	res, err := HandlerFunc(func(ev Event) { got = ev }).RunSync(context.Background(), Event{Name: "deploy"})
	if got.Name != "deploy" || err != nil || res != (Result{}) {
		t.Errorf("got %q, result %+v, error %v", got.Name, res, err)
	}
}
