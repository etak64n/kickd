package main

// kickd help: the usage of kickd and of each subcommand.

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// A command is a subcommand of kickd, as kickd help describes it.
type command struct {
	name    string
	args    string // what follows the name on the command line
	summary string
}

// commands are the subcommands of kickd, in the order that kickd help
// lists them.
var commands = []command{
	{"run", "", "Run the agent in the foreground, or as a service when started by one"},
	{"event", "NAME [KEY=VALUE ...] [--data JSON] [--wait] [--timeout DURATION] [--json]", "Fire an event; --wait waits for the run and exits 0 only if it succeeds"},
	{"events", "[--json]", "List the events and what fires them"},
	{"history", "[--event NAME] [--status STATUS] [--limit N] [--json]", "Show the runs, newest first, so the runs that run or wait come first"},
	{"show", "RUN_ID [--json]", "Show one run, including its output"},
	{"cancel", "RUN_ID", "Cancel a queued run, or stop a running one"},
	{"status", "[--json]", "Show whether the agent is running and how many runs wait"},
	{"check", "", "Validate the config and print a summary"},
	{"init", "", "Write an example config and an example events file"},
	{"service", "ACTION", "Install or control the service that runs the agent; ACTION is install, uninstall, start, stop, restart or status"},
	{"licenses", "", "Print the licenses of kickd and of the software it includes"},
	{"version", "", "Print the version"},
	{"help", "[COMMAND]", "Show this help, or the help of one command"},
}

// opsCommands are the subcommands that runOps handles.
var opsCommands = map[string]bool{"event": true, "events": true, "history": true, "show": true, "cancel": true, "status": true}

// configHelp ends the help of kickd: where kickd finds its config file.
const configHelp = `
The user who runs kickd decides its config file:
  ~/.kickd/config.yaml                            for a user
  /etc/kickd/config.yaml                          for the whole machine: as root on Linux
  /Library/Application Support/kickd/config.yaml  for the whole machine: as root on macOS
  C:\ProgramData\kickd\config.yaml                for the whole machine: as an administrator on Windows
kickd also reads the events of the other .yaml and .yml files next to the
config file. kickd service works on the service of the user, or on the
service of the whole machine as root or as an administrator.

kickd help COMMAND shows the usage and the flags of one command.
`

// usage returns the help of kickd: every subcommand with its arguments,
// and where kickd finds its config file.
func usage() string {
	var b strings.Builder
	b.WriteString("kickd - run named events from cron, webhooks, file changes or the command line\n\nUsage:\n")
	for _, c := range commands {
		// The summaries start in column 43, and a summary whose command
		// line reaches that column starts on the next line.
		left := strings.TrimRight(fmt.Sprintf("  kickd %-8s%s", c.name, c.args), " ")
		if len(left) < 42 {
			fmt.Fprintf(&b, "%-43s%s\n", left, c.summary)
		} else {
			fmt.Fprintf(&b, "%s\n%43s%s\n", left, "", c.summary)
		}
	}
	b.WriteString(configHelp)
	return b.String()
}

// commandHelp returns the help of the subcommand name: its command line,
// what it does, and the flags of fs when fs has any.
func commandHelp(name string, fs *flag.FlagSet) (string, error) {
	var c *command
	for i := range commands {
		if commands[i].name == name {
			c = &commands[i]
		}
	}
	if c == nil {
		return "", fmt.Errorf("unknown command %q; kickd help lists the commands", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n\n%s.\n", strings.TrimSpace("kickd "+c.name+" "+c.args), c.summary)
	n := 0
	if fs != nil {
		fs.VisitAll(func(*flag.Flag) { n++ })
	}
	if n > 0 {
		b.WriteString("\nFlags:\n")
		tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
		fs.VisitAll(func(f *flag.Flag) {
			value, text := flag.UnquoteUsage(f)
			arg := "--" + f.Name
			if value != "" {
				arg += " " + value
			}
			switch f.DefValue {
			case "", "false", "0", "0s":
			default:
				text += " (default " + f.DefValue + ")"
			}
			fmt.Fprintf(tw, "  %s\t%s\n", arg, text)
		})
		tw.Flush()
	}
	return b.String(), nil
}

// help prints the help of kickd, or the help of the one subcommand that
// args names, and returns the exit code.
func help(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0:
		fmt.Fprint(stdout, usage())
		return exitOK
	case len(args) > 1:
		fmt.Fprintln(stderr, "kickd: kickd help takes one command:", strings.Join(args, " "))
		return exitUsage
	case opsCommands[args[0]]:
		// The flags of these subcommands live in their flag sets, which
		// print the help when they see --help.
		return runOps("", []string{args[0], "--help"}, stdout, stderr)
	}
	text, err := commandHelp(args[0], nil)
	if err != nil {
		fmt.Fprintln(stderr, "kickd:", err)
		return exitUsage
	}
	fmt.Fprint(stdout, text)
	return exitOK
}

// isHelp reports whether arg asks for help.
func isHelp(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}
