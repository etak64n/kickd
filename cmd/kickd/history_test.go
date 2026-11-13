package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"
)

// A restart waits in the queue for its delay, and kickd history shows when
// it starts in its DETAIL column.
func TestHistoryShowsWhenAWaitingRestartStarts(t *testing.T) {
	_, cfg := writeConfig(t, `
events:
  - name: server
    command: `+helperCommand()+`
    env: {KICKD_CMD_HELPER: boom}
    restart: on-failure
    restart_delay: 1h
    triggers: [{type: manual}]
`)
	stop := startAgent(t, cfg)
	defer stop()
	if code, out, errOut := ops(t, cfg, "event", "server", "--wait", "--timeout", "20s"); code != exitFailed {
		t.Fatalf("kickd event server --wait: exit %d\n%s%s", code, out, errOut)
	}
	// The run failed, so a restart of server waits an hour.
	var restart struct {
		ID        int64  `json:"runId"`
		Trigger   string `json:"trigger"`
		NotBefore string `json:"notBefore"`
	}
	for deadline := time.Now().Add(10 * time.Second); restart.ID == 0; time.Sleep(50 * time.Millisecond) {
		_, out, _ := ops(t, cfg, "history", "--event", "server", "--status", "queued", "--json")
		var queued []json.RawMessage
		if err := json.Unmarshal([]byte(out), &queued); err != nil {
			t.Fatalf("kickd history --json: %v\n%s", err, out)
		}
		if len(queued) == 1 {
			if err := json.Unmarshal(queued[0], &restart); err != nil {
				t.Fatal(err)
			}
		}
		if restart.ID == 0 && time.Now().After(deadline) {
			t.Fatalf("no restart of server waits:\n%s", out)
		}
	}
	at, err := time.Parse(time.RFC3339, restart.NotBefore)
	if restart.Trigger != "restart" || err != nil {
		t.Fatalf("the waiting run: %+v, %v", restart, err)
	}
	_, out, _ := ops(t, cfg, "history", "--event", "server")
	want := regexp.MustCompile(fmt.Sprintf(`(?m)^%d\s+server\s+restart\s+queued\s+-\s+-\s+-\s+starts at %s\s*$`,
		restart.ID, regexp.QuoteMeta(at.Local().Format("2006-01-02 15:04:05"))))
	if !want.MatchString(out) {
		t.Errorf("kickd history does not say when run %d starts, at %s:\n%s", restart.ID, at.Local().Format("2006-01-02 15:04:05"), out)
	}
}
