//go:build e2e && darwin

package macos

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestShowCutsTheOutputAt64KB(t *testing.T) {
	t.Parallel()
	h := newHome(t, "output")
	h.start()
	id := h.fire("export")
	h.waitForRun(id)
	if out := h.must("show", strconv.FormatInt(id, 10)); !hasLine(out, "--- output truncated at 64 KB ---") {
		t.Errorf("kickd show ends with:\n%s", out[max(0, len(out)-300):])
	}
}

func TestTheOutputKeepsTheLinesOfStandardOutputAndStandardErrorWhole(t *testing.T) {
	t.Parallel()
	h := newHome(t, "output")
	h.start()
	r := h.waitForRun(h.fire("check-hosts"))
	whole := regexp.MustCompile(`^host \d+ (answers|is slow)$`)
	lines := 0
	for _, line := range strings.Split(strings.TrimRight(r.Output, "\r\n"), "\n") {
		lines++
		if line = strings.TrimRight(line, "\r"); !whole.MatchString(line) {
			t.Errorf("a broken line: %q", line)
		}
	}
	if lines != 400 {
		t.Errorf("%d lines, want 400", lines)
	}
}
