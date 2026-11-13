package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/etak64n/kickd/internal/queue"
	"github.com/etak64n/kickd/internal/runner"
)

// An event under concurrency: queue keeps at most 1000 runs waiting, so
// kickd event records a firing past them as dropped and exits with code 1.
func TestEventExitsWithOneWhenItsRunIsDropped(t *testing.T) {
	dir, cfg := writeConfig(t, `
events:
  - name: build
    concurrency: queue
    command: `+helperCommand()+`
    env: {KICKD_CMD_HELPER: deploy}
    triggers: [{type: manual}]
`)
	st, err := queue.Open(filepath.Join(dir, "kickd.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range runner.MaxQueuedPerEvent {
		if _, _, err := st.Enqueue(context.Background(), queue.Run{RequestID: fmt.Sprintf("build-%d", i), Event: "build", Trigger: "manual",
			TriggerID: "manual", Payload: []byte(`{}`), Attempt: 1}, 0); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	code, out, errOut := ops(t, cfg, "event", "build")
	want := fmt.Sprintf("event build already has %d queued runs; run %d was recorded as dropped", runner.MaxQueuedPerEvent, runner.MaxQueuedPerEvent+1)
	if code != exitFailed || !strings.Contains(errOut, want) {
		t.Errorf("kickd event build: exit %d\n%s%s", code, out, errOut)
	}
}
