package config

import (
	"io"
	"log/slog"
	"testing"

	kevent "github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/trigger"
)

// FuzzParse feeds Parse with generated configs. Parse must not crash, and
// the webhook server must accept every path of a config that Parse
// accepts, because the agent registers them when it starts and at every
// reload.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		ExampleConfig("linux", false) + ExampleEvents("linux"),
		"events:\n  - name: a\n    command: [x]\n    triggers: [{type: webhook, path: /hooks/a, token: t}]\n",
		"events:\n  - name: a\n    command: [x]\n    triggers: [{type: webhook, path: /hooks/, secret: s, methods: [POST]}]\n",
		"events:\n  - name: a\n    command: 'echo hi'\n    params: [{name: ref, default: main}]\n    triggers: [{type: manual}, {type: cron, schedule: '*/5 * * * *'}]\n",
		"events:\n  - name: a\n    command: [x]\n    triggers: [{type: manual}]\n  - name: b\n    command: [y]\n    triggers: [{type: after, event: a, status: [failed]}]\n",
	} {
		f.Add([]byte(seed))
	}
	dir := f.TempDir()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := Parse(data, dir)
		if err != nil {
			return
		}
		s := trigger.NewWebhookServer("127.0.0.1:0", 0, discard)
		for _, e := range cfg.Events {
			for _, tr := range e.Triggers {
				if tr.Type != TriggerWebhook {
					continue
				}
				route := trigger.WebhookRoute{Event: e.Name, Path: tr.Path, Methods: tr.Methods, Token: tr.Token, Secret: tr.Secret}
				if err := s.Register(route, kevent.HandlerFunc(func(kevent.Event) {})); err != nil {
					t.Fatalf("Parse accepted a webhook path that the server rejects: %v", err)
				}
			}
		}
	})
}
