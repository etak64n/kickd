package trigger

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

func TestWebhookRefusesARequestFromABrowser(t *testing.T) {
	for _, header := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode"} {
		t.Run(header, func(t *testing.T) {
			srv, h, logs := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/hooks/deploy"})
			code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy", "", map[string]string{header: "https://example.com"})
			if code != http.StatusForbidden || out["error"] != "requests from web browsers are not accepted" {
				t.Fatalf("code = %d, body = %v", code, out)
			}
			select {
			case ev := <-h.events:
				t.Fatalf("the event fired: %+v", ev)
			default:
			}
			if l := logs.find("Webhook rejected"); len(l) != 1 || l[0]["reason"] != "browser_request" {
				t.Fatalf("log = %v", l)
			}
		})
	}
}

// A request without the token gets 401 even when its body is too large,
// because kickd checks the token before it reads the body.
func TestWebhookChecksTheTokenBeforeItReadsTheBody(t *testing.T) {
	srv, _, _ := newTestServer(t, 10, WebhookRoute{Event: "deploy", Path: "/hooks/deploy", Token: "t0ken"})
	body := strings.Repeat("x", 100)
	if code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy", body, nil); code != http.StatusUnauthorized {
		t.Fatalf("without the token: code = %d, body = %v", code, out)
	}
	if code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy", body, map[string]string{"Authorization": "Bearer t0ken"}); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("with the token: code = %d, body = %v", code, out)
	}
}

// The payload leaves out the signature, which would let anyone who reads
// the payload send the same body again.
func TestWebhookPayloadLeavesOutTheSignature(t *testing.T) {
	srv, h, _ := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/hooks/deploy", Secret: "s3cret"})
	body := `{"ref":"main"}`
	sig := Signature([]byte(body), "s3cret")
	code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy", body, map[string]string{
		"X-Hub-Signature-256": sig, "X-Kickd-Signature": sig, "X-Hub-Signature": "sha1=0", "X-Delivery": "42",
	})
	if code != http.StatusAccepted {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	ev := <-h.events
	for k := range ev.Webhook.Headers {
		if strings.Contains(strings.ToLower(k), "signature") {
			t.Errorf("the payload has the header %s", k)
		}
	}
	if ev.Webhook.Headers["X-Delivery"] != "42" {
		t.Errorf("headers = %v", ev.Webhook.Headers)
	}
}

// A run that the database cannot record gets 503, not the 409 of a full
// queue, and not an empty 200.
func TestWebhookAnswers503WhenTheRunCannotBeRecorded(t *testing.T) {
	srv, h, _ := newTestServer(t, 0,
		WebhookRoute{Event: "deploy", Path: "/hooks/deploy"},
		WebhookRoute{Event: "deploy", Path: "/hooks/deploy-and-wait", Wait: true})
	h.status = event.Failed
	if code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy", "", nil); code != http.StatusServiceUnavailable || out["status"] != "failed" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	h.err = errors.New("database is locked")
	if code, out, _ := do(t, "POST", srv.URL+"/hooks/deploy-and-wait", "", nil); code != http.StatusServiceUnavailable || out["status"] != "failed" {
		t.Fatalf("with wait: code = %d, body = %v", code, out)
	}
}

// Paths that Go's router cannot parse, or that conflict, are errors of
// Register and do not stop the agent.
func TestRegisterRefusesAPathThatTheRouterRejects(t *testing.T) {
	s := NewWebhookServer("127.0.0.1:0", 0, slog.Default())
	if err := s.Register(WebhookRoute{Event: "a", Path: "/hooks/{id"}, newStub()); err == nil {
		t.Fatal("Register accepted /hooks/{id")
	}
	if err := s.Register(WebhookRoute{Event: "b", Path: "/x/{a}"}, newStub()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(WebhookRoute{Event: "c", Path: "/x/{b}"}, newStub()); err == nil {
		t.Fatal("Register accepted a path that conflicts with another")
	}
	if err := s.Register(WebhookRoute{Event: "d", Path: "/hooks/deploy"}, newStub()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want the 2 paths that registered", s.Len())
	}
}

func TestListenReportsAnAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s := NewWebhookServer(ln.Addr().String(), 0, slog.Default())
	if err := s.Listen(); err == nil || !strings.Contains(err.Error(), "webhook listen") {
		t.Fatalf("Listen = %v", err)
	}
}

// The listener lets the given number of connections in at once, and a
// connection that closes makes room for the next.
func TestLimitListenerWaitsForAConnectionToClose(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(inner, 1)
	defer ln.Close()
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	for range 2 {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	first := <-accepted
	select {
	case <-accepted:
		t.Fatal("a second connection was accepted while the first was open")
	case <-time.After(200 * time.Millisecond):
	}
	first.Close()
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("the second connection was not accepted after the first closed")
	}
}

// Closing the listener ends an Accept that waits for room.
func TestLimitListenerCloseEndsAWaitingAccept(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(inner, 1)
	c, err := net.Dial("tcp", inner.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	held, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	done := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	ln.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept still waits after Close")
	}
}
