package main

import (
	"strings"
	"testing"
)

// The unit that kickd service install writes on Linux starts kickd again
// soon after it exits, and a per-user unit starts with the systemd of the
// user.
func TestServiceConfigSystemdUnit(t *testing.T) {
	for _, user := range []bool{false, true} {
		c := serviceConfig("/etc/kickd/config.yaml", "kickd", user)
		unit, _ := c.Option["SystemdScript"].(string)
		want := "WantedBy=multi-user.target\n"
		if user {
			want = "WantedBy=default.target\n"
		}
		if !strings.Contains(unit, "}}RestartSec=5\n") || !strings.HasSuffix(unit, want) {
			t.Errorf("user %v: the unit is\n%s", user, unit)
		}
		if c.Option["Restart"] != "always" {
			t.Errorf("user %v: Restart = %v", user, c.Option["Restart"])
		}
	}
}
