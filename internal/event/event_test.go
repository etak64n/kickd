package event

import (
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
