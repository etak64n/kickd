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
}

// NewWebhookServer creates a server that listens on addr once Run is called.
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

// Register adds a route that dispatches to h.
func (s *WebhookServer) Register(r WebhookRoute, h event.Handler) error {
	if s.routes[r.Path] {
		return fmt.Errorf("webhook path %q is registered twice", r.Path)
	}
	s.routes[r.Path] = true
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
		if len(methods) > 0 && !methods[req.Method] {
			log.Warn("Webhook rejected", "reason", "method_not_allowed", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
			s.writeJSON(w, log, http.StatusMethodNotAllowed, map[string]any{"requestId": id, "error": "method not allowed"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, s.maxBody))
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
		if r.Token != "" {
			if reason := checkToken(req, r.Token); reason != "" {
				log.Warn("Webhook authentication failed", "reason", reason, "authMethod", "token", "method", req.Method, "path", req.URL.Path, "remoteAddr", req.RemoteAddr)
				s.writeJSON(w, log, http.StatusUnauthorized, map[string]any{"requestId": id, "error": "unauthorized"})
				return
			}
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
				Headers:    flatten(req.Header, "Authorization", "X-Kickd-Token"),
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
			case err != nil:
				// The caller disconnected; the job keeps running.
				if sw, ok := w.(*statusWriter); ok {
					sw.gone = true
				}
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
		status := http.StatusAccepted
		if st == event.Dropped {
			status = http.StatusConflict
		}
		s.writeJSON(w, log, status, map[string]any{"requestId": id, "event": r.Event, "status": string(st)})
	})
	return nil
}

// Run listens and serves until ctx is done.
func (s *WebhookServer) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("webhook listen: %w", err)
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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

func (w *statusWriter) code() int {
	switch {
	case w.gone:
		return statusClientGone
	case w.status == 0:
		return http.StatusOK
	}
	return w.status
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
