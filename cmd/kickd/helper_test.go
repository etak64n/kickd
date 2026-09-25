package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the command of the test events: with
// KICKD_CMD_HELPER set, it acts as the event named there and exits. The
// tests then need no shell syntax, which differs between sh and cmd.
func TestMain(m *testing.M) {
	switch os.Getenv("KICKD_CMD_HELPER") {
	case "":
		os.Exit(m.Run())
	case "deploy":
		fmt.Println("deploying " + os.Getenv("KICKD_DATA_REF"))
		os.Exit(0)
	case "boom":
		fmt.Fprintln(os.Stderr, "broken")
		os.Exit(7)
	case "slow":
		if n, _ := strconv.Atoi(os.Getenv("KICKD_ATTEMPT")); n >= 2 {
			os.Exit(0)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		os.Exit(99)
	}
}

// helperCommand is a YAML flow sequence that runs the test binary.
func helperCommand() string {
	return "['" + strings.ReplaceAll(os.Args[0], "'", "''") + "']"
}

func eventConfig() string {
	cmd := helperCommand()
	return `
events:
  - name: deploy
    description: Deploy the app
    params: [{name: ref, default: main}]
    command: ` + cmd + `
    env: {KICKD_CMD_HELPER: deploy}
    triggers: [{type: manual}]
  - name: boom
    command: ` + cmd + `
    env: {KICKD_CMD_HELPER: boom}
    triggers: [{type: cron, schedule: "@yearly"}, {type: manual}]
  - name: slow
    on_interrupt: rerun
    command: ` + cmd + `
    env: {KICKD_CMD_HELPER: slow}
    triggers: [{type: manual}]
  - name: nightly
    command: ` + cmd + `
    env: {KICKD_CMD_HELPER: deploy}
    triggers: [{type: cron, schedule: "@daily"}]
`
}
