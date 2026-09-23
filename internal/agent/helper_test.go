package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the command of the test events: with
// KICKD_AGENT_HELPER set, it appends a line to HELPER_OUT and exits. The
// tests then need no shell syntax, which differs between sh and cmd.
func TestMain(m *testing.M) {
	switch os.Getenv("KICKD_AGENT_HELPER") {
	case "":
		os.Exit(m.Run())
	case "append":
		appendLine(os.Getenv("HELPER_TEXT"))
	case "append-event":
		appendLine(fmt.Sprintf("ref=%s event=%s trigger=%s", os.Getenv("KICKD_DATA_REF"), os.Getenv("KICKD_EVENT"), os.Getenv("KICKD_TRIGGER")))
	case "attempt":
		appendLine("attempt=" + os.Getenv("KICKD_ATTEMPT"))
		if n, _ := strconv.Atoi(os.Getenv("KICKD_ATTEMPT")); n < 2 {
			time.Sleep(30 * time.Second)
		}
	default:
		os.Exit(99)
	}
	os.Exit(0)
}

func appendLine(s string) {
	f, err := os.OpenFile(os.Getenv("HELPER_OUT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(98)
	}
	fmt.Fprintln(f, s)
	f.Close()
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// helperEvent returns the command and env keys of an event that runs the
// test binary in mode, appending to out. extra adds KEY, VALUE pairs to env.
func helperEvent(mode, out string, extra ...string) string {
	s := "    command: [" + quote(os.Args[0]) + "]\n" +
		"    env:\n      KICKD_AGENT_HELPER: " + mode + "\n      HELPER_OUT: " + quote(out) + "\n"
	for i := 0; i+1 < len(extra); i += 2 {
		s += "      " + extra[i] + ": " + quote(extra[i+1]) + "\n"
	}
	return s
}
