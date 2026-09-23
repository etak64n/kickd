package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/etak64n/kickd", Version: v}}
	}
	cases := []struct {
		name string
		set  string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"ldflags wins", "v1.2.3", info("v9.9.9"), true, "v1.2.3"},
		{"go install", "dev", info("v0.1.0"), true, "v0.1.0"},
		{"pseudo-version", "dev", info("v0.0.0-20260924000000-392a28a0b1c2+dirty"), true, "v0.0.0-20260924000000-392a28a0b1c2+dirty"},
		{"no vcs info", "dev", info("(devel)"), true, "dev"},
		{"empty", "dev", info(""), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	}
	for _, c := range cases {
		if got := resolveVersion(c.set, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
