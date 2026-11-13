package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/queue"
)

// restartWaits describes the restarts that env scheduled: the wait of each,
// and how long the run before it lasted.
func restartWaits(env *dispEnv) string {
	s := ""
	for _, rec := range env.rec.find("Restart scheduled") {
		id, _ := rec["afterRunId"].(int64)
		r, _ := env.store.GetRun(context.Background(), id)
		s += fmt.Sprintf("\n  run %d lasted %s, and the restart after it waits %v ms", id, r.Duration, rec["delayMs"])
	}
	return s
}

// A command that fails at once is restarted after waits that double, and
// no wait is longer than restart_max_delay.
func TestRestartWaitsNoLongerThanRestartMaxDelay(t *testing.T) {
	spec := restarting(helperJob("exit"), RestartAlways, 100*time.Millisecond, time.Second)
	env := newDispatcher(t, spec)
	env.d.Handler(spec.Name).Dispatch(fired(spec.Name))
	want := []int64{100, 200, 400, 800, 1000, 1000}
	got := env.rec.waitFor(t, "Restart scheduled", len(want), 30*time.Second)
	for i, ms := range want {
		if got[i]["delayMs"] != ms {
			t.Fatalf("restart %d waits %v ms, want %d ms:%s", i+1, got[i]["delayMs"], ms, restartWaits(env))
		}
	}
}

// A run that lasts restart_max_delay or longer starts the waits again from
// restart_delay, after waits that had grown.
func TestARunAsLongAsRestartMaxDelayStartsTheWaitsAgainFromRestartDelay(t *testing.T) {
	const delay, maxDelay = 100 * time.Millisecond, 600 * time.Millisecond
	quick := restarting(helperJob("exit"), RestartAlways, delay, maxDelay)
	env := newDispatcher(t, quick)
	env.d.Handler(quick.Name).Dispatch(fired(quick.Name))
	env.rec.waitFor(t, "Restart scheduled", 2, 15*time.Second)
	// From the next run on, the command runs for 700 ms, longer than
	// restart_max_delay.
	env.d.Configure([]Spec{restarting(helperJob("short"), RestartAlways, delay, maxDelay)})
	deadline := time.Now().Add(20 * time.Second)
	for {
		restarts := env.rec.find("Restart scheduled")
		for i, rec := range restarts {
			id, _ := rec["afterRunId"].(int64)
			r, err := env.store.GetRun(context.Background(), id)
			if err != nil || r.Duration < maxDelay {
				continue
			}
			before, _ := restarts[max(i-1, 0)]["delayMs"].(int64)
			if i == 0 || before <= delay.Milliseconds() || rec["delayMs"] != delay.Milliseconds() {
				t.Fatalf("the restart after run %d, which lasted %s, waits %v ms, want %d ms after a longer wait:%s",
					id, r.Duration, rec["delayMs"], delay.Milliseconds(), restartWaits(env))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no run lasted %s:%s", maxDelay, restartWaits(env))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// restart: on-failure starts the event again after a run that its timeout
// stopped, which failed.
func TestRestartOnFailureRestartsAfterATimeout(t *testing.T) {
	spec := restarting(helperJob("sleep"), RestartOnFailure, time.Hour, time.Hour)
	spec.Timeout = 300 * time.Millisecond
	env := newDispatcher(t, spec)
	ev := fired(spec.Name)
	ev.RequestID = "slow"
	env.d.Handler(spec.Name).Dispatch(ev)
	got := env.rec.waitFor(t, "Restart scheduled", 1, 15*time.Second)
	r := runOf(t, env.store, "slow")
	if r.Status != queue.StatusFailed || r.Reason != "timeout" || got[0]["afterRunId"] != r.ID {
		t.Errorf("restart %v after run %d, %s with the reason %s", got[0], r.ID, r.Status, r.Reason)
	}
}

// restart: on-failure starts the event again after a run whose command
// could not start, which failed.
func TestRestartOnFailureRestartsAfterAFailureToStart(t *testing.T) {
	spec := restarting(helperJob("short"), RestartOnFailure, time.Hour, time.Hour)
	spec.Command = []string{filepath.Join(t.TempDir(), "missing")}
	env := newDispatcher(t, spec)
	ev := fired(spec.Name)
	ev.RequestID = "missing"
	env.d.Handler(spec.Name).Dispatch(ev)
	got := env.rec.waitFor(t, "Restart scheduled", 1, 15*time.Second)
	r := runOf(t, env.store, "missing")
	if r.Status != queue.StatusFailed || r.Reason != "start_failed" || got[0]["afterRunId"] != r.ID {
		t.Errorf("restart %v after run %d, %s with the reason %s", got[0], r.ID, r.Status, r.Reason)
	}
}
