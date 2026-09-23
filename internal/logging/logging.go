// Package logging builds the agent logger. Records follow the logging
// guideline this project uses, which mirrors AWS Lambda and CloudWatch
// Logs: JSON lines that start with timestamp, level, message, requestId
// and service, or tab separated text with the columns timestamp,
// requestId, level, message and the remaining keys as one JSON object.
package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// ServiceName is the value of the service key.
const ServiceName = "kickd"

// Levels beyond the four that slog defines. Together they form the six
// levels of the guideline.
const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

// Formats.
const (
	FormatAuto = "auto"
	FormatJSON = "json"
	FormatText = "text"
)

const tsLayout = "2006-01-02T15:04:05.000Z07:00"

// Options configure New.
type Options struct {
	Level      string
	Format     string // auto, json or text; auto picks text on a terminal
	File       string // empty writes to Stderr
	MaxSizeMB  int
	MaxBackups int
	// Console additionally writes text lines to Stderr while File is set,
	// when Stderr is a terminal. It is meant for foreground runs.
	Console bool
	// Stderr defaults to os.Stderr.
	Stderr io.Writer
}

// New builds a logger from o. Close the returned closer at exit.
func New(o Options) (*slog.Logger, io.Closer, error) {
	level, ok := ParseLevel(o.Level)
	if !ok {
		level = slog.LevelInfo
	}
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	var (
		handlers []slog.Handler
		closer   io.Closer = nopCloser{}
	)
	if o.File != "" {
		rf, err := openRotating(o.File, int64(o.MaxSizeMB)<<20, o.MaxBackups, stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		closer = rf
		format := o.Format
		if format == "" || format == FormatAuto {
			format = FormatJSON
		}
		handlers = append(handlers, newHandler(rf, level, format, false))
		if o.Console && isTerminal(stderr) {
			handlers = append(handlers, newHandler(stderr, level, FormatText, colorEnabled(stderr)))
		}
	} else {
		format := o.Format
		if format == "" || format == FormatAuto {
			format = FormatJSON
			if isTerminal(stderr) {
				format = FormatText
			}
		}
		handlers = append(handlers, newHandler(stderr, level, format, format == FormatText && colorEnabled(stderr)))
	}
	var h slog.Handler = handlers[0]
	if len(handlers) > 1 {
		h = fanout(handlers)
	}
	return slog.New(h).With("service", ServiceName), closer, nil
}

// ParseLevel converts a level name to a slog level.
func ParseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	case "fatal":
		return LevelFatal, true
	}
	return slog.LevelInfo, false
}

// LevelName returns the guideline name of a level.
func LevelName(l slog.Level) string {
	switch {
	case l >= LevelFatal:
		return "FATAL"
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	case l >= slog.LevelDebug:
		return "DEBUG"
	default:
		return "TRACE"
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// syncWriter serialises writes from handlers that share one destination.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// handler writes records in the guideline's JSON or text shape.
type handler struct {
	out    *syncWriter
	level  slog.Level
	format string
	color  bool
	attrs  []slog.Attr
	prefix string
}

func newHandler(w io.Writer, level slog.Level, format string, color bool) *handler {
	return &handler{out: &syncWriter{w: w}, level: level, format: format, color: color}
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	n := *h
	n.attrs = slices.Clip(h.attrs)
	for _, a := range as {
		n.attrs = appendAttr(n.attrs, h.prefix, a)
	}
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.prefix = h.prefix + name + "."
	return &n
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	attrs := slices.Clone(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		attrs = appendAttr(attrs, h.prefix, a)
		return true
	})
	attrs = dedupe(attrs)
	if r.Level >= slog.LevelError && r.PC != 0 && !hasKey(attrs, "location") {
		attrs = append(attrs, slog.String("location", location(r.PC)))
	}
	reqID, service := "-", ""
	rest := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		switch a.Key {
		case "requestId":
			reqID = a.Value.String()
		case "service":
			service = a.Value.String()
		default:
			rest = append(rest, a)
		}
	}
	var b bytes.Buffer
	if h.format == FormatText {
		h.writeText(&b, r, reqID, rest)
	} else {
		writeJSONRecord(&b, r, reqID, service, rest)
	}
	h.out.mu.Lock()
	defer h.out.mu.Unlock()
	_, err := h.out.w.Write(b.Bytes())
	return err
}

// appendAttr resolves a and appends it, flattening groups into dotted keys.
// A group with an empty key is inlined, which lets helpers such as Err
// return several keys as one argument.
func appendAttr(dst []slog.Attr, prefix string, a slog.Attr) []slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			dst = appendAttr(dst, p, g)
		}
		return dst
	}
	if a.Key == "" {
		return dst
	}
	a.Key = prefix + a.Key
	return append(dst, a)
}

// dedupe keeps one attribute per key: the last value, at the position
// where the key first appeared.
func dedupe(attrs []slog.Attr) []slog.Attr {
	index := make(map[string]int, len(attrs))
	out := attrs[:0]
	for _, a := range attrs {
		if i, ok := index[a.Key]; ok {
			out[i] = a
			continue
		}
		index[a.Key] = len(out)
		out = append(out, a)
	}
	return out
}

func hasKey(attrs []slog.Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// location formats the call site as file:function:line.
func location(pc uintptr) string {
	frames := runtime.CallersFrames([]uintptr{pc})
	f, _ := frames.Next()
	fn := f.Function
	if i := strings.LastIndex(fn, "/"); i >= 0 {
		fn = fn[i+1:]
	}
	if i := strings.Index(fn, "."); i >= 0 {
		fn = fn[i+1:]
	}
	return filepath.Base(f.File) + ":" + fn + ":" + strconv.Itoa(f.Line)
}

func writeJSONRecord(b *bytes.Buffer, r slog.Record, reqID, service string, rest []slog.Attr) {
	b.WriteString(`{"timestamp":`)
	writeJSON(b, r.Time.UTC().Format(tsLayout))
	b.WriteString(`,"level":`)
	writeJSON(b, LevelName(r.Level))
	b.WriteString(`,"message":`)
	writeJSON(b, r.Message)
	b.WriteString(`,"requestId":`)
	writeJSON(b, reqID)
	if service != "" {
		b.WriteString(`,"service":`)
		writeJSON(b, service)
	}
	for _, a := range rest {
		b.WriteByte(',')
		writeJSON(b, a.Key)
		b.WriteByte(':')
		writeValue(b, a.Value)
	}
	b.WriteString("}\n")
}

func (h *handler) writeText(b *bytes.Buffer, r slog.Record, reqID string, rest []slog.Attr) {
	if h.color {
		b.WriteString(levelColor(r.Level))
	}
	b.WriteString(r.Time.UTC().Format(tsLayout))
	b.WriteByte('\t')
	b.WriteString(oneLine(reqID))
	b.WriteByte('\t')
	b.WriteString(LevelName(r.Level))
	b.WriteByte('\t')
	b.WriteString(oneLine(r.Message))
	b.WriteByte('\t')
	if len(rest) == 0 {
		b.WriteByte('-')
	} else {
		b.WriteByte('{')
		for i, a := range rest {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, a.Key)
			b.WriteByte(':')
			writeValue(b, a.Value)
		}
		b.WriteByte('}')
	}
	if h.color {
		b.WriteString(colorReset)
	}
	b.WriteByte('\n')
}

// writeJSON appends v as JSON without escaping <, > and &, which keeps
// paths and commands readable.
func writeJSON(b *bytes.Buffer, v any) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		_ = enc.Encode(fmt.Sprint(v))
	}
	if n := b.Len(); n > 0 && b.Bytes()[n-1] == '\n' {
		b.Truncate(n - 1)
	}
}

func writeValue(b *bytes.Buffer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		writeJSON(b, v.String())
	case slog.KindInt64:
		b.WriteString(strconv.FormatInt(v.Int64(), 10))
	case slog.KindUint64:
		b.WriteString(strconv.FormatUint(v.Uint64(), 10))
	case slog.KindFloat64:
		f := v.Float64()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			writeJSON(b, strconv.FormatFloat(f, 'g', -1, 64))
			return
		}
		b.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
	case slog.KindBool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case slog.KindDuration:
		b.WriteString(strconv.FormatInt(v.Duration().Milliseconds(), 10))
	case slog.KindTime:
		writeJSON(b, v.Time().UTC().Format(tsLayout))
	default:
		if err, ok := v.Any().(error); ok {
			writeJSON(b, err.Error())
			return
		}
		writeJSON(b, v.Any())
	}
}

func oneLine(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

// Terminal colors per level, as the guideline assigns them.
const colorReset = "\033[0m"

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "\033[0;31m"
	case l >= slog.LevelWarn:
		return "\033[0;33m"
	case l >= slog.LevelInfo:
		return "\033[0;36m"
	default:
		return "\033[0;90m"
	}
}

// isTerminal reports whether w is a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTTY(f)
}

// colorEnabled reports whether escape sequences may be written to w.
// NO_COLOR turns colors off, following the no-color.org convention.
func colorEnabled(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && isTTY(f) && enableVT(f)
}

// fanout sends every record to several handlers.
type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var first error
	for _, h := range f {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (f fanout) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
