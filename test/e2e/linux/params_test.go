//go:build e2e && linux

package linux

import (
	"strings"
	"testing"
)

func TestEventPassesAParameterGivenAsKeyAndValue(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	h.start()
	if r := h.waitForRun(h.fire("deploy", "ref=v1.2")); r.line("ref") != "v1.2" {
		t.Errorf("deploy: %s\n%s", r.Status, r.Output)
	}
}

func TestEventTakesParametersFromJSONInData(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	h.start()
	r := h.waitForRun(h.fire("deploy", "--data", `{"ref":"v1.3","target":"production"}`))
	if r.line("ref") != "v1.3" || r.line("target") != "production" {
		t.Errorf("deploy: %s\n%s", r.Status, r.Output)
	}
}

func TestAParameterThatTheFiringLacksGetsItsDefault(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	h.start()
	if r := h.waitForRun(h.fire("deploy", "ref=v1.2")); r.line("target") != "staging" {
		t.Errorf("deploy: %s\n%s", r.Status, r.Output)
	}
}

func TestEventRefusesAFiringWithoutARequiredParameter(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	if r := h.kickd("event", "deploy"); r.code != 2 || !strings.Contains(r.stderr, `event "deploy": parameter ref is required`) {
		t.Errorf("kickd event deploy: exit %d\n%s", r.code, r.stderr)
	}
}

func TestEventRefusesAParameterThatTheEventDoesNotDeclare(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	r := h.kickd("event", "deploy", "ref=v1.2", "color=blue")
	if r.code != 2 || !strings.Contains(r.stderr, `event "deploy": unknown parameter color (declared: ref, target)`) {
		t.Errorf("kickd event deploy color=blue: exit %d\n%s", r.code, r.stderr)
	}
}

func TestEventRefusesAnEventThatIsNotDefined(t *testing.T) {
	t.Parallel()
	h := newHome(t, "params")
	r := h.kickd("event", "release")
	if r.code != 2 || !strings.Contains(r.stderr, `event "release" is not defined`) || !strings.Contains(r.stderr, "(defined: deploy)") {
		t.Errorf("kickd event release: exit %d\n%s", r.code, r.stderr)
	}
}
