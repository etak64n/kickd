package main

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func runHelp(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := help(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestHelpListsEveryCommandWithItsSummary(t *testing.T) {
	code, out, _ := runHelp(t)
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, c := range commands {
		if !regexp.MustCompile(`(?m)^  kickd `+c.name+`\b`).MatchString(out) || !strings.Contains(out, c.summary) {
			t.Errorf("kickd help has no %s with %q:\n%s", c.name, c.summary, out)
		}
	}
}

func TestHelpStartsEverySummaryInOneColumn(t *testing.T) {
	_, out, _ := runHelp(t)
	for _, c := range commands {
		i := strings.Index(out, c.summary)
		if i < 0 {
			t.Errorf("kickd help has no summary of %s", c.name)
			continue
		}
		line := out[strings.LastIndex(out[:i], "\n")+1 : i]
		if len(line) != 43 {
			t.Errorf("the summary of %s starts in column %d", c.name, len(line))
		}
	}
}

func TestHelpOfACommandShowsItsCommandLineAndWhatItDoes(t *testing.T) {
	code, out, _ := runHelp(t, "show")
	if code != exitOK || !strings.HasPrefix(out, "Usage: kickd show RUN_ID [--json]\n\nShow one run, including its output.\n") {
		t.Errorf("kickd help show: exit %d\n%s", code, out)
	}
}

func TestHelpOfACommandListsItsFlagsWithTheirValuesAndDefaults(t *testing.T) {
	_, out, _ := runHelp(t, "history")
	for _, want := range []string{
		`(?m)^  --event NAME\s+only the runs of the event NAME$`,
		`(?m)^  --json\s+print JSON$`,
		`(?m)^  --limit N\s+show N runs at most \(default 20\)$`,
		`(?m)^  --status STATUS\s+only the runs with this STATUS: queued, running`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("kickd help history has no line %s:\n%s", want, out)
		}
	}
}

func TestHelpOfACommandWithoutFlagsHasNoFlags(t *testing.T) {
	if _, out, _ := runHelp(t, "check"); strings.Contains(out, "Flags:") {
		t.Errorf("kickd help check:\n%s", out)
	}
}

// The command line of each subcommand in kickd help names the flags that
// the subcommand takes, and no others.
func TestTheCommandLineOfEachCommandNamesItsFlags(t *testing.T) {
	for _, c := range commands {
		if !opsCommands[c.name] {
			continue
		}
		_, out, _ := runHelp(t, c.name)
		got := regexp.MustCompile(`(?m)^  (--[a-z]+)`).FindAllStringSubmatch(out, -1)
		var flags []string
		for _, m := range got {
			flags = append(flags, m[1])
		}
		named := regexp.MustCompile(`--[a-z]+`).FindAllString(c.args, -1)
		slices.Sort(flags)
		slices.Sort(named)
		if !slices.Equal(flags, named) {
			t.Errorf("%s takes %v, and its command line names %v", c.name, flags, named)
		}
	}
}

func TestTheHelpFlagOfACommandShowsItsHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runOps("", []string{"event", "deploy", "--help"}, &out, &errOut); code != exitOK || !strings.HasPrefix(out.String(), "Usage: kickd event NAME") {
		t.Errorf("kickd event deploy --help: exit %d\n%s%s", code, out.String(), errOut.String())
	}
}

func TestHelpOfAnUnknownCommandSaysThatHelpListsTheCommands(t *testing.T) {
	if code, _, errOut := runHelp(t, "nope"); code != exitUsage || !strings.Contains(errOut, `unknown command "nope"; kickd help lists the commands`) {
		t.Errorf("kickd help nope: exit %d %q", code, errOut)
	}
}

func TestAnUnknownFlagSaysWhichHelpListsTheFlags(t *testing.T) {
	_, cfg := writeConfig(t, eventConfig())
	if code, _, errOut := ops(t, cfg, "history", "--nope"); code != exitUsage || !strings.Contains(errOut, "unknown flag --nope; kickd help history lists the flags") {
		t.Errorf("kickd history --nope: exit %d %q", code, errOut)
	}
}
