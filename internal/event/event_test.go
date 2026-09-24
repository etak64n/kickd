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
				strings.HasPrefix(name, "KICKD_WEBHOOK_") || strings.HasPrefix(name, "KICKD_FILE_")
			own := strings.HasPrefix(name, "KICKD_"+strings.ToUpper(kind)+"_")
			if other && !own && v != "" {
				t.Errorf("%s: %s = %q, want empty", kind, name, v)
			}
		}
		if values["KICKD_DATA"] != `{"ref":"main"}` || values["KICKD_DATA_REF"] != "main" {
			t.Errorf("%s: KICKD_DATA %q, KICKD_DATA_REF %q", kind, values["KICKD_DATA"], values["KICKD_DATA_REF"])
		}
	}
	for _, old := range []string{"KICKD_EVENT_DATA", "KICKD_SOURCE"} {
		if slices.Contains(first, old) {
			t.Errorf("%s is still set", old)
		}
	}
}
