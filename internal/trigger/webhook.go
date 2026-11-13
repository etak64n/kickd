package trigger

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/etak64n/kickd/internal/event"
	"github.com/etak64n/kickd/internal/logging"
)

// DefaultMaxBody bounds webhook request bodies when the config sets none.
const DefaultMaxBody = 1 << 20

// RequestIDHeader carries the request ID in both directions.
const RequestIDHeader = "X-Request-ID"

// statusClientGone marks requests whose caller disconnected before the
// response, as nginx does with 499.
const statusClientGone = 499

// Limits of the server, so that clients that hold connections open or send
// slowly cannot use up the memory and the file descriptors of the agent.
const (
	maxConns          = 256              // connections served at once; more wait to be accepted
	maxHeaderBytes    = 64 << 10         // the request line and the headers
	readHeaderTimeout = 10 * time.Second // to receive the request line and the headers
	readBodyTimeout   = 30 * time.Second // to receive the body
	idleTimeout       = 60 * time.Second // a kept-alive connection without a request
)

// signatureHeaders carry the signature of the body. The payload leaves
// them out, as it leaves out the token, because a signature lets anyone
// who reads it send the same body again.
var signatureHeaders = []string{"X-Hub-Signature-256", "X-Hub-Signature", "X-Kickd-Signature"}

// WebhookRoute describes one URL path that fires a named event.
type WebhookRoute struct {
	Event   string
	Path    string
	Methods []string
	Token   string
	Secret  string
	Wait    bool
	// Params are the declared parameter names of the event; query values
	// with these names become the event data. Required ones must be given.
	Params   []string
	Required []string
}

// WebhookServer serves every webhook trigger of one configuration.
type WebhookServer struct {
	addr    string
	maxBody int64
	logger  *slog.Logger
	mux     *http.ServeMux
	routes  map[string]bool
	ln      net.Listener
}

// NewWebhookServer creates a server for addr. Listen binds the address,
// and Run serves on it.
func NewWebhookServer(addr string, maxBody int64, logger *slog.Logger) *WebhookServer {
	if maxBody <= 0 {
		maxBody = DefaultMaxBody
	}
	s := &WebhookServer{
		addr:    addr,
		maxBody: maxBody,
		logger:  logger.With("trigger", event.KindWebhook),
		mux:     http.NewServeMux(),
		routes:  map[string]bool{},
	}
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := io.WriteString(w, "ok\n"); err != nil {
			s.logger.Debug("Response write failed", "requestId", requestIDOf(req.Context()), logging.Err(err))
		}
	})
	return s
}

// Handler returns the HTTP handler. It assigns the request ID, serves the
// route and logs one "Request completed" line per request.
func (s *WebhookServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		id := requestIDFrom(req.Header.Get(RequestIDHeader))
		w.Header().Set(RequestIDHeader, id)
		sw := &statusWriter{ResponseWriter: w}
		s.mux.ServeHTTP(sw, req.WithContext(context.WithValue(req.Context(), requestIDKey{}, id)))
		level := slog.LevelInfo
		if req.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		s.logger.Log(context.Background(), level, "Request completed",
			"requestId", id, "method", req.Method, "path", req.URL.Path,
			"status", sw.code(), "durationMs", time.Since(start).Milliseconds(), "remoteAddr", req.RemoteAddr)
	})
}

// Len returns the number of registered routes.
func (s *WebhookServer) Len() int { return len(s.routes) }

// Register adds a route that dispatches to h. A path that the router
// rejects is an error, as a path registered twice is.
func (s *WebhookServer) Register(r WebhookRoute, h event.Handler) (err error) {
	if s.routes[r.Path] {
		return fmt.Errorf("webhook path %q is registered twice", r.Path)
	}
	// ServeMux panics on a pattern that it cannot parse or that conflicts
	// with another; the config rules out such paths, and this keeps a
	// path that slips through from stopping the agent.
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("webhook path %q: %v", r.Path, p)
		}
	}()
	methods := map[string]bool{}
	for _, m := range r.Methods {
		methods[strings.ToUpper(m)] = true
	}
	authMethod := "none"
	switch {
	case r.Token != "" && r.Secret != "":
		authMethod = "token+signature"
	case r.Token != "":
		authMethod = "token"
	case r.Secret != "":
		authMethod = "signature"
	}
	routeLog := s.logger.With("event", r.Event)

	s.mux.HandleFunc(r.Path, func(w http.ResponseWriter, req *http.Request) {
		id := requestIDOf(req.Context())
		log := routeLog.With("requestId", id)
		// A web page that the user opens can make the browser send a
		// request to 127.0.0.1, and browsers mark such requests with these
		// headers. Programs that call webhooks do not send them.
		if fromBrowser(req) {
			log.Warn("Webhook rejected", "reason", "browser_request", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
			s.writeJSON(w, log, http.StatusForbidden, map[string]any{"requestId": id, "error": "requests from web browsers are not accepted"})
			return
		}
		if len(methods) > 0 && !methods[req.Method] {
			log.Warn("Webhook rejected", "reason", "method_not_allowed", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
			s.writeJSON(w, log, http.StatusMethodNotAllowed, map[string]any{"requestId": id, "error": "method not allowed"})
			return
		}
		// The token is in the headers or the query, so a request without
		// it is refused before kickd reads its body.
		if r.Token != "" {
			if reason := checkToken(req, r.Token); reason != "" {
				log.Warn("Webhook authentication failed", "reason", reason, "authMethod", "token", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
				s.writeJSON(w, log, http.StatusUnauthorized, map[string]any{"requestId": id, "error": "unauthorized"})
				return
			}
		}
		body, err := readBody(w, req, s.maxBody)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				log.Warn("Webhook rejected", "reason", "body_too_large", "method", req.Method, "path", req.URL.Path, "thresholdBytes", s.maxBody, "remoteAddr", req.RemoteAddr)
				s.writeJSON(w, log, http.StatusRequestEntityTooLarge, map[string]any{"requestId": id, "error": "body too large"})
				return
			}
			log.Warn("Webhook body read failed", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr, logging.Err(err))
			s.writeJSON(w, log, http.StatusBadRequest, map[string]any{"requestId": id, "error": "cannot read body"})
			return
		}
		if r.Secret != "" {
			if reason, header := checkSignature(req, body, r.Secret); reason != "" {
				attrs := []any{"reason", reason, "authMethod", "signature", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr}
				if header != "" {
					attrs = append(attrs, "header", header)
				}
				log.Warn("Webhook authentication failed", attrs...)
				s.writeJSON(w, log, http.StatusUnauthorized, map[string]any{"requestId": id, "error": "unauthorized"})
				return
			}
		}
		query := req.URL.Query()
		data := map[string]string{}
		for _, name := range r.Params {
			if v := query.Get(name); v != "" {
				data[name] = v
			}
		}
		for _, name := range r.Required {
			if _, ok := data[name]; !ok {
				log.Warn("Webhook rejected", "reason", "missing_parameter", "detail", name, "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
				s.writeJSON(w, log, http.StatusBadRequest, map[string]any{"requestId": id, "error": "missing parameter " + name})
				return
			}
		}
		ev := event.Event{
			RequestID: id,
			Name:      r.Event,
			Data:      data,
			Trigger:   event.KindWebhook,
			TriggerID: "webhook:" + r.Path,
			Time:      time.Now(),
			Webhook: &event.WebhookInfo{
				Method:     req.Method,
				Path:       req.URL.Path,
				RemoteAddr: req.RemoteAddr,
				Headers:    flatten(req.Header, append([]string{"Authorization", "X-Kickd-Token"}, signatureHeaders...)...),
				Query:      flatten(query, "token"),
				Body:       string(body),
			},
		}
		log.Info("Webhook received", "method", req.Method, "path", req.URL.Path, "bytes", len(body), "authMethod", authMethod, "wait", r.Wait, "remoteAddr", req.RemoteAddr)
		if r.Wait {
			res, err := h.RunSync(req.Context(), ev)
			switch {
			case errors.Is(err, event.ErrBusy):
				s.writeJSON(w, log, http.StatusConflict, map[string]any{"requestId": id, "event": r.Event, "status": "skipped"})
			case errors.Is(err, event.ErrQueueFull):
				s.writeJSON(w, log, http.StatusConflict, map[string]any{"requestId": id, "event": r.Event, "status": "dropped"})
			case err != nil && req.Context().Err() != nil:
				// The caller disconnected; the job keeps running.
				if sw, ok := w.(*statusWriter); ok {
					sw.gone = true
				}
			case err != nil:
				// The run could not be recorded, so it does not run.
				s.writeJSON(w, log, http.StatusServiceUnavailable, map[string]any{"requestId": id, "event": r.Event, "status": "failed", "error": "the run could not be recorded"})
			default:
				status := http.StatusOK
				if res.ExitCode != 0 || res.Error != "" {
					status = http.StatusInternalServerError
				}
				s.writeJSON(w, log, status, map[string]any{
					"requestId":       id,
					"event":           r.Event,
					"exitCode":        res.ExitCode,
					"durationMs":      res.Duration.Milliseconds(),
					"output":          res.Output,
					"outputTruncated": res.Truncated,
					"error":           res.Error,
				})
			}
			return
		}
		st := h.Dispatch(ev)
		switch st {
		case event.Dropped:
			s.writeJSON(w, log, http.StatusConflict, map[string]any{"requestId": id, "event": r.Event, "status": string(st)})
		case event.Failed:
			s.writeJSON(w, log, http.StatusServiceUnavailable, map[string]any{"requestId": id, "event": r.Event, "status": string(st), "error": "the run could not be recorded"})
		default:
			s.writeJSON(w, log, http.StatusAccepted, map[string]any{"requestId": id, "event": r.Event, "status": string(st)})
		}
	})
	s.routes[r.Path] = true
	return nil
}

// fromBrowser reports whether a web browser sent req: browsers add Origin
// to the requests that pages make, and the Sec-Fetch headers to every
// request.
func fromBrowser(req *http.Request) bool {
	for _, h := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode"} {
		if req.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// readBody reads the body of req, up to limit bytes, within
// readBodyTimeout. The deadline ends once the body is read, because the
// server keeps reading the connection while the handler waits, to notice
// a caller that leaves, and a deadline that passed would end the request.
func readBody(w http.ResponseWriter, req *http.Request, limit int64) ([]byte, error) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(readBodyTimeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, limit))
	_ = rc.SetReadDeadline(time.Time{})
	return body, err
}

// Listen binds the address of the server, so that an address in use is
// found before the agent starts anything. Run calls it when it was not
// called.
func (s *WebhookServer) Listen() error {
	if s.ln != nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("webhook listen: %w", err)
	}
	s.ln = newLimitListener(ln, maxConns)
	return nil
}

// SetListener makes the server serve on ln, which the caller bound.
func (s *WebhookServer) SetListener(ln net.Listener) {
	s.ln = newLimitListener(ln, maxConns)
}

// Run serves until ctx is done.
func (s *WebhookServer) Run(ctx context.Context) error {
	if err := s.Listen(); err != nil {
		return err
	}
	ln := s.ln
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          stdlog.New(serverErrorWriter{s.logger}, "", 0),
	}
	addr := ln.Addr().String()
	s.logger.Info("Webhook server listening", "listen", addr, "count", len(s.routes))

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("Webhook server shutdown incomplete", "listen", addr, logging.Err(err))
		}
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("webhook serve: %w", err)
	}
	<-done
	s.logger.Debug("Webhook server stopped", "listen", addr)
	return nil
}

func (s *WebhookServer) writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Debug("Response write failed", logging.Err(err))
	}
}

// serverErrorWriter routes net/http's own error log into the agent log
// under one fixed message.
type serverErrorWriter struct{ l *slog.Logger }

func (e serverErrorWriter) Write(p []byte) (int, error) {
	e.l.Warn("HTTP server error", "detail", strings.TrimSpace(string(p)))
	return len(p), nil
}

// statusWriter records the status code for the completion line.
type statusWriter struct {
	http.ResponseWriter
	status int
	gone   bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) code() int {
	switch {
	case w.gone:
		return statusClientGone
	case w.status == 0:
		return http.StatusOK
	}
	return w.status
}

// limitListener accepts a connection only while fewer than the capacity
// of slots are open. The others wait in the backlog of the OS.
type limitListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newLimitListener(ln net.Listener, n int) *limitListener {
	return &limitListener{Listener: ln, slots: make(chan struct{}, n), done: make(chan struct{})}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitConn{Conn: c, release: func() { <-l.slots }}, nil
}

// Close also ends an Accept that waits for a slot.
func (l *limitListener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() { close(l.done) })
	return err
}

// limitConn gives its slot back when it closes, once.
type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

type requestIDKey struct{}

func requestIDOf(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// requestIDFrom returns the caller's request ID when it is safe to log,
// or a new one. Accepting the caller's ID lets its logs and kickd's logs
// share one requestId.
func requestIDFrom(h string) string {
	if n := len(h); n == 0 || n > 128 {
		return event.NewID()
	}
	for _, c := range h {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':'
		if !ok {
			return event.NewID()
		}
	}
	return h
}

// checkToken accepts the token as a bearer token, in the X-Kickd-Token
// header or in the token query parameter. It returns a reason on failure.
func checkToken(req *http.Request, token string) string {
	got := ""
	if a := req.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		got = strings.TrimPrefix(a, "Bearer ")
	}
	if got == "" {
		got = req.Header.Get("X-Kickd-Token")
	}
	if got == "" {
		got = req.URL.Query().Get("token")
	}
	switch {
	case got == "":
		return "token_missing"
	case subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1:
		return "token_mismatch"
	}
	return ""
}

// checkSignature verifies an HMAC-SHA256 signature of the body, in the
// format GitHub uses: "sha256=<hex>" in X-Hub-Signature-256 (or
// X-Kickd-Signature). It returns a reason and the header it read.
func checkSignature(req *http.Request, body []byte, secret string) (reason, header string) {
	header = "X-Hub-Signature-256"
	sig := req.Header.Get(header)
	if sig == "" {
		header = "X-Kickd-Signature"
		sig = req.Header.Get(header)
	}
	if sig == "" {
		return "signature_missing", ""
	}
	sig = strings.TrimPrefix(sig, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return "signature_mismatch", header
	}
	return "", header
}

// Signature computes the signature header value for body, for clients and
// tests.
func Signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func flatten(values map[string][]string, drop ...string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		skip := false
		for _, d := range drop {
			if strings.EqualFold(k, d) {
				skip = true
				break
			}
		}
		if skip || len(v) == 0 {
			continue
		}
		out[k] = v[0]
	}
	return out
}
