package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rules for the paths and tokens of webhook triggers and for the
// patterns of file triggers.
func TestWebhookAndPatternErrors(t *testing.T) {
	noLogEnv(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := filepath.ToSlash(filepath.Join(dir, "d"))
	const ok = "\n    command: [x]\n"
	hook := func(fields string) string {
		return "events:\n  - name: a" + ok + "    triggers: [{type: webhook, " + fields + "}]"
	}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"space in a path", hook("path: '/hooks/deploy '"), `path "/hooks/deploy " may contain only letters, digits`},
		{"wildcard in a path", hook("path: '/hooks/{id}'"), "may contain only letters, digits"},
		{"method in a path", hook("path: 'POST /hooks'"), "must start with /"},
		{"percent in a path", hook("path: '/hooks%2Fdeploy'"), "may contain only letters, digits"},
		{"empty segment", hook("path: '/hooks//deploy'"), "must not contain an empty segment"},
		{"dot segment", hook("path: '/hooks/./deploy'"), "must not contain an empty segment (//), . or .."},
		{"dot dot segment", hook("path: '/hooks/../deploy'"), "must not contain an empty segment (//), . or .."},
		{"example token", hook("path: /h, token: 'replace-with-a-long-random-string'"), "token is the example value replace-with-a-long-random-string"},
		{"example secret", hook("path: /h, secret: 'replace-with-a-long-random-string'"), "secret is the example value replace-with-a-long-random-string"},
		{"parameter named token", "events:\n  - name: a\n    params: [{name: token}]" + ok + "    triggers: [{type: webhook, path: /h}]", "cannot have a parameter named token"},
		{"malformed include", "events:\n  - name: a" + ok + "    triggers: [{type: file, path: " + d + ", include: ['*.[ch']}]", `include pattern "*.[ch" is malformed`},
		{"malformed exclude", "events:\n  - name: a" + ok + "    triggers: [{type: file, path: " + d + ", exclude: ['build/[']}]", `exclude pattern "build/[" is malformed`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.body), dir)
			if err == nil {
				t.Fatalf("expected an error containing %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestWebhookPathsThatLoad(t *testing.T) {
	noLogEnv(t)
	for _, p := range []string{"/", "/hooks/", "/hooks/deploy", "/v1/a.b_c~d-e/", "/Hooks/Deploy-2"} {
		body := "events:\n  - name: a\n    command: [x]\n    triggers: [{type: webhook, path: '" + p + "'}]"
		if _, err := Parse([]byte(body), t.TempDir()); err != nil {
			t.Errorf("path %s: %v", p, err)
		}
	}
}

// A parameter named token is fine for an event without a webhook trigger,
// whose query cannot carry a token.
func TestAParameterNamedTokenWithoutAWebhookTriggerLoads(t *testing.T) {
	noLogEnv(t)
	body := "events:\n  - name: a\n    params: [{name: token}]\n    command: [x]\n    triggers: [{type: manual}]"
	if _, err := Parse([]byte(body), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// The check of the documented examples accepts the example token, so the
// problem is recognizable apart from others.
func TestTheExampleTokenIsAProblemOfItsOwn(t *testing.T) {
	noLogEnv(t)
	body := "events:\n  - name: a\n    command: [x]\n    triggers: [{type: webhook, path: /h, token: '" + ExampleToken + "'}]"
	_, err := Parse([]byte(body), t.TempDir())
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || !errors.Is(ve.Problems[0], errExampleValue) {
		t.Fatalf("err = %v", err)
	}
}
