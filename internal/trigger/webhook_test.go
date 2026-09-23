package trigger

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/etak64n/kickd/internal/event"
)

type stubHandler struct {
	events chan event.Event
	status event.DispatchStatus
	result event.Result
	err    error
	block  bool // RunSync waits for the caller to leave
}

func newStub() *stubHandler {
	return &stubHandler{events: make(chan event.Event, 8), status: event.Queued}
}

func (s *stubHandler) Dispatch(ev event.Event) event.DispatchStatus {
	s.events <- ev
	return s.status
}

func (s *stubHandler) RunSync(ctx context.Context, ev event.Event) (event.Result, error) {
	s.events <- ev
	if s.block {
		<-ctx.Done()
		return event.Result{}, ctx.Err()
	}
	return s.result, s.err
}

// capture records log lines for assertions.
type capture struct {
	mu      sync.Mutex
	records []map[string]any
}

type captureHandler struct {
	c     *capture
	attrs []slog.Attr
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }
func (h *captureHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &captureHandler{c: h.c, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{"message": r.Message, "level": r.Level}
	add := func(a slog.Attr) {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindGroup {
			for _, g := range v.Group() {
				m[g.Key] = g.Value.Any()
			}
			return
		}
		m[a.Key] = v.Any()
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	h.c.mu.Lock()
	h.c.records = append(h.c.records, m)
	h.c.mu.Unlock()
	return nil
}

func (c *capture) find(message string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, m := range c.records {
		if m["message"] == message {
			out = append(out, m)
		}
	}
	return out
}

func newTestServer(t *testing.T, maxBody int64, routes ...WebhookRoute) (*httptest.Server, *stubHandler, *capture) {
	t.Helper()
	c := &capture{}
	s := NewWebhookServer("127.0.0.1:0", maxBody, slog.New(&captureHandler{c: c}))
	h := newStub()
	for _, r := range routes {
		if err := s.Register(r, h); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, h, c
}

func do(t *testing.T, method, url, body string, headers map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, res.Header
}

func TestWebhookDispatch(t *testing.T) {
	srv, h, logs := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/hooks/deploy"})
	code, out, hdr := do(t, "POST", srv.URL+"/hooks/deploy?ref=main&token=zzz", `{"x":1}`, map[string]string{
		"Authorization": "Bearer hidden", "X-Custom": "yes",
	})
	if code != http.StatusAccepted || out["status"] != "queued" || out["event"] != "deploy" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	ev := <-h.events
	if ev.RequestID == "" || hdr.Get(RequestIDHeader) != ev.RequestID || out["requestId"] != ev.RequestID {
		t.Errorf("request ID: header %q, body %v, event %q", hdr.Get(RequestIDHeader), out["requestId"], ev.RequestID)
	}
	if ev.Name != "deploy" || ev.Trigger != event.KindWebhook || ev.TriggerID != "webhook:/hooks/deploy" {
		t.Errorf("event = %+v", ev)
	}
	w := ev.Webhook
	if w == nil || w.Method != "POST" || w.Body != `{"x":1}` || w.Query["ref"] != "main" || w.Headers["X-Custom"] != "yes" {
		t.Errorf("webhook info = %+v", w)
	}
	if _, ok := w.Headers["Authorization"]; ok {
		t.Error("Authorization header must be stripped")
	}
	if _, ok := w.Query["token"]; ok {
		t.Error("token query must be stripped")
	}
	received := logs.find("Webhook received")
	if len(received) != 1 || received[0]["requestId"] != ev.RequestID || received[0]["bytes"] != int64(7) || received[0]["authMethod"] != "none" {
		t.Errorf("received = %v", received)
	}
	completed := logs.find("Request completed")
	if len(completed) != 1 || completed[0]["status"] != int64(202) || completed[0]["requestId"] != ev.RequestID || completed[0]["path"] != "/hooks/deploy" {
		t.Errorf("completed = %v", completed)
	}
}

func TestWebhookRequestIDFromCaller(t *testing.T) {
	srv, h, _ := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h"})
	_, out, hdr := do(t, "POST", srv.URL+"/h", "", map[string]string{RequestIDHeader: "gh-delivery-7f3a"})
	if ev := <-h.events; ev.RequestID != "gh-delivery-7f3a" || hdr.Get(RequestIDHeader) != "gh-delivery-7f3a" || out["requestId"] != "gh-delivery-7f3a" {
		t.Errorf("caller ID not kept: event %q, header %q, body %v", ev.RequestID, hdr.Get(RequestIDHeader), out["requestId"])
	}
	_, _, hdr = do(t, "POST", srv.URL+"/h", "", map[string]string{RequestIDHeader: "bad id with spaces"})
	if ev := <-h.events; ev.RequestID == "bad id with spaces" || len(ev.RequestID) != 16 || hdr.Get(RequestIDHeader) != ev.RequestID {
		t.Errorf("unsafe ID must be replaced: event %q, header %q", ev.RequestID, hdr.Get(RequestIDHeader))
	}
}

func TestWebhookDispatchConflict(t *testing.T) {
	srv, h, _ := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h"})
	h.status = event.Dropped
	if code, out, _ := do(t, "POST", srv.URL+"/h", "", nil); code != http.StatusConflict || out["status"] != "dropped" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
}

func TestWebhookMethods(t *testing.T) {
	srv, _, logs := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h", Methods: []string{"POST"}})
	if code, _, _ := do(t, "GET", srv.URL+"/h", "", nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET code = %d", code)
	}
	if code, _, _ := do(t, "POST", srv.URL+"/h", "", nil); code != http.StatusAccepted {
		t.Fatalf("POST code = %d", code)
	}
	if r := logs.find("Webhook rejected"); len(r) != 1 || r[0]["reason"] != "method_not_allowed" || r[0]["level"] != slog.LevelWarn {
		t.Errorf("rejected = %v", r)
	}
}

func TestWebhookToken(t *testing.T) {
	srv, _, logs := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h", Token: "s3cret"})
	cases := []struct {
		name    string
		url     string
		headers map[string]string
		want    int
		reason  string
	}{
		{"none", "/h", nil, 401, "token_missing"},
		{"wrong bearer", "/h", map[string]string{"Authorization": "Bearer nope"}, 401, "token_mismatch"},
		{"bearer", "/h", map[string]string{"Authorization": "Bearer s3cret"}, 202, ""},
		{"header", "/h", map[string]string{"X-Kickd-Token": "s3cret"}, 202, ""},
		{"query", "/h?token=s3cret", nil, 202, ""},
		{"wrong query", "/h?token=s3cre", nil, 401, "token_mismatch"},
	}
	var reasons []string
	for _, c := range cases {
		if code, _, _ := do(t, "POST", srv.URL+c.url, "", c.headers); code != c.want {
			t.Errorf("%s: code = %d, want %d", c.name, code, c.want)
		}
		if c.reason != "" {
			reasons = append(reasons, c.reason)
		}
	}
	var got []string
	for _, r := range logs.find("Webhook authentication failed") {
		got = append(got, r["reason"].(string))
		if r["authMethod"] != "token" {
			t.Errorf("authMethod = %v", r["authMethod"])
		}
	}
	if strings.Join(got, ",") != strings.Join(reasons, ",") {
		t.Errorf("reasons = %v, want %v", got, reasons)
	}
	for _, l := range logs.find("Webhook authentication failed") {
		for k, v := range l {
			if s, ok := v.(string); ok && strings.Contains(s, "s3cre") {
				t.Errorf("token leaked into %s: %v", k, l)
			}
		}
	}
}

func TestWebhookSignature(t *testing.T) {
	srv, _, logs := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h", Secret: "topsecret"})
	body := `{"ref":"refs/heads/main"}`
	if code, _, _ := do(t, "POST", srv.URL+"/h", body, nil); code != 401 {
		t.Errorf("unsigned: code = %d", code)
	}
	if code, _, _ := do(t, "POST", srv.URL+"/h", body, map[string]string{"X-Hub-Signature-256": "sha256=deadbeef"}); code != 401 {
		t.Errorf("bad signature: code = %d", code)
	}
	good := Signature([]byte(body), "topsecret")
	if code, _, _ := do(t, "POST", srv.URL+"/h", body, map[string]string{"X-Hub-Signature-256": good}); code != 202 {
		t.Errorf("good signature: code = %d", code)
	}
	if code, _, _ := do(t, "POST", srv.URL+"/h", body+" ", map[string]string{"X-Kickd-Signature": good}); code != 401 {
		t.Errorf("tampered body: code = %d", code)
	}
	failed := logs.find("Webhook authentication failed")
	if len(failed) != 3 || failed[0]["reason"] != "signature_missing" || failed[1]["reason"] != "signature_mismatch" || failed[1]["header"] != "X-Hub-Signature-256" || failed[2]["header"] != "X-Kickd-Signature" {
		t.Errorf("failed = %v", failed)
	}
}

func TestWebhookWait(t *testing.T) {
	srv, h, _ := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h", Wait: true})
	h.result = event.Result{ExitCode: 0, Output: "done\n", Duration: 1500 * time.Millisecond}
	code, out, _ := do(t, "POST", srv.URL+"/h", "", nil)
	if code != 200 || out["exitCode"] != float64(0) || out["output"] != "done\n" || out["durationMs"] != float64(1500) || out["requestId"] == "" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	<-h.events
	h.result = event.Result{ExitCode: 2}
	if code, out, _ := do(t, "POST", srv.URL+"/h", "", nil); code != 500 || out["exitCode"] != float64(2) {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	<-h.events
	h.err = event.ErrBusy
	if code, out, _ := do(t, "POST", srv.URL+"/h", "", nil); code != 409 || out["status"] != "skipped" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
	<-h.events
	h.err = event.ErrQueueFull
	if code, out, _ := do(t, "POST", srv.URL+"/h", "", nil); code != 409 || out["status"] != "dropped" {
		t.Fatalf("code = %d, body = %v", code, out)
	}
}

func TestWebhookWaitCallerGone(t *testing.T) {
	srv, h, logs := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h", Wait: true})
	h.block = true
	client := &http.Client{Timeout: 200 * time.Millisecond}
	if _, err := client.Post(srv.URL+"/h", "application/json", strings.NewReader("{}")); err == nil {
		t.Fatal("expected the client to time out")
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(logs.find("Request completed")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no completion line")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c := logs.find("Request completed"); c[0]["status"] != int64(statusClientGone) {
		t.Errorf("completed = %v", c[0])
	}
}

func TestWebhookBodyTooLarge(t *testing.T) {
	srv, _, logs := newTestServer(t, 16, WebhookRoute{Event: "j", Path: "/h"})
	if code, _, _ := do(t, "POST", srv.URL+"/h", strings.Repeat("x", 100), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d", code)
	}
	if r := logs.find("Webhook rejected"); len(r) != 1 || r[0]["reason"] != "body_too_large" || r[0]["thresholdBytes"] != int64(16) {
		t.Errorf("rejected = %v", r)
	}
}

func TestWebhookHealthAndUnknown(t *testing.T) {
	srv, _, logs := newTestServer(t, 0, WebhookRoute{Event: "j", Path: "/h"})
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.TrimSpace(string(b)) != "ok" {
		t.Fatalf("healthz: %d %q", res.StatusCode, b)
	}
	if code, _, _ := do(t, "POST", srv.URL+"/nope", "", nil); code != 404 {
		t.Fatalf("unknown path code = %d", code)
	}
	completed := logs.find("Request completed")
	if len(completed) != 2 || completed[0]["level"] != slog.LevelDebug || completed[1]["status"] != int64(404) || completed[1]["level"] != slog.LevelInfo {
		t.Errorf("completed = %v", completed)
	}
}

func TestWebhookDuplicateRoute(t *testing.T) {
	s := NewWebhookServer("127.0.0.1:0", 0, slog.Default())
	if err := s.Register(WebhookRoute{Event: "a", Path: "/h"}, newStub()); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(WebhookRoute{Event: "b", Path: "/h"}, newStub()); err == nil {
		t.Fatal("second registration must fail")
	}
}

func TestWebhookRunAndShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := NewWebhookServer(addr, 0, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not come up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The port is free again, so a second server can bind it (reload case).
	s2 := NewWebhookServer(addr, 0, logger)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- s2.Run(ctx2) }()
	time.Sleep(100 * time.Millisecond)
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("rebind after shutdown: %v", err)
	}
}

func TestWebhookParamsFromQuery(t *testing.T) {
	srv, h, logs := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/d", Params: []string{"ref", "env"}, Required: []string{"env"}})
	if code, _, _ := do(t, "POST", srv.URL+"/d?ref=v2&env=prod&other=x", "", nil); code != http.StatusAccepted {
		t.Fatalf("code = %d", code)
	}
	ev := <-h.events
	if len(ev.Data) != 2 || ev.Data["ref"] != "v2" || ev.Data["env"] != "prod" {
		t.Errorf("data = %v; only declared params become data", ev.Data)
	}
	code, out, _ := do(t, "POST", srv.URL+"/d?ref=v2", "", nil)
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "env") {
		t.Fatalf("missing required: %d %v", code, out)
	}
	if r := logs.find("Webhook rejected"); len(r) != 1 || r[0]["reason"] != "missing_parameter" || r[0]["detail"] != "env" {
		t.Errorf("rejected = %v", r)
	}
}

func TestWebhookEmptyQueryValueIsMissing(t *testing.T) {
	srv, _, _ := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/d", Params: []string{"env"}, Required: []string{"env"}})
	code, out, _ := do(t, "POST", srv.URL+"/d?env=", "", nil)
	if code != http.StatusBadRequest || out["error"] != "missing parameter env" {
		t.Fatalf("an empty value must count as missing: %d %v", code, out)
	}
}

func TestWebhookPayloadHeaders(t *testing.T) {
	srv, h, _ := newTestServer(t, 0, WebhookRoute{Event: "deploy", Path: "/d", Token: "tok"})
	req, err := http.NewRequest("POST", srv.URL+"/d", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Kickd-Token", "tok")
	req.Header.Add("X-Multi", "first")
	req.Header.Add("X-Multi", "second")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	ev := <-h.events
	if _, ok := ev.Webhook.Headers["X-Kickd-Token"]; ok {
		t.Error("the X-Kickd-Token header must be stripped from the payload")
	}
	if ev.Webhook.Headers["X-Multi"] != "first" {
		t.Errorf("X-Multi = %q, want the first value", ev.Webhook.Headers["X-Multi"])
	}
}
