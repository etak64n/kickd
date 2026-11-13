package trigger

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FuzzWebhookRequest sends generated requests to a server with a route
// that needs a token, a route that needs a signature and a route that
// needs neither. No request may crash the handler, and no request may fire
// the event of a route without the credentials of that route.
func FuzzWebhookRequest(f *testing.F) {
	f.Add("POST", "/hooks/deploy", "Bearer t0ken", "", "ref=main", []byte(`{"ref":"main"}`))
	f.Add("POST", "/hooks/deploy", "", "", "token=t0ken", []byte(""))
	f.Add("POST", "/hooks/signed", "", Signature([]byte("x"), "s3cret"), "", []byte("x"))
	f.Add("GET", "/hooks/open/sub", "Bearer nope", "sha256=00", "a=b&a=c", []byte("y"))
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.Fuzz(func(t *testing.T, method, path, auth, sig, query string, body []byte) {
		req, err := http.NewRequest(method, "http://kickd"+path+"?"+query, bytes.NewReader(body))
		if err != nil {
			return
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		s := NewWebhookServer("127.0.0.1:0", 64, discard)
		h := newStub()
		for _, r := range []WebhookRoute{
			{Event: "deploy", Path: "/hooks/deploy", Token: "t0ken", Params: []string{"ref"}},
			{Event: "signed", Path: "/hooks/signed", Secret: "s3cret"},
			{Event: "open", Path: "/hooks/open/"},
		} {
			if err := s.Register(r, h); err != nil {
				t.Fatal(err)
			}
		}
		s.Handler().ServeHTTP(httptest.NewRecorder(), req)
		select {
		case ev := <-h.events:
			switch ev.Name {
			case "deploy":
				if !strings.Contains(auth, "t0ken") && req.URL.Query().Get("token") != "t0ken" {
					t.Fatalf("deploy fired without the token: auth %q, query %q", auth, query)
				}
			case "signed":
				if !strings.EqualFold(sig, Signature(body, "s3cret")) {
					t.Fatalf("signed fired without the signature of its body: %q", sig)
				}
			}
		default:
		}
	})
}
