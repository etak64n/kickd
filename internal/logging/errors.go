package logging

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

const maxFrames = 50

// Err returns the errorType and errorMessage keys of err as one inlined
// attribute, so that it can be passed among other key-value pairs.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.Group("",
		slog.String("errorType", ErrorType(err)),
		slog.String("errorMessage", err.Error()),
	)
}

// ErrorType names the type of err, qualified by its package (fs.PathError,
// exec.Error). Generic wrappers created by fmt.Errorf and errors.Join are
// skipped so that the name describes the underlying failure.
func ErrorType(err error) string {
	outer := ""
	for e := err; e != nil; {
		t := reflect.TypeOf(e)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		name := t.String()
		if outer == "" {
			outer = name
		}
		if !genericWrapper(t) {
			return name
		}
		next := errors.Unwrap(e)
		if next == nil {
			if m, ok := e.(interface{ Unwrap() []error }); ok && len(m.Unwrap()) > 0 {
				next = m.Unwrap()[0]
			}
		}
		e = next
	}
	return outer
}

func genericWrapper(t reflect.Type) bool {
	switch t.PkgPath() + "." + t.Name() {
	case "fmt.wrapError", "fmt.wrapErrors", "errors.joinError":
		return true
	}
	return false
}

// Panic returns the error keys of a recovered panic, including stackTrace.
// stack is the output of runtime/debug.Stack taken in the deferred call.
func Panic(recovered any, stack []byte) slog.Attr {
	typ := "panic"
	if err, ok := recovered.(error); ok {
		typ = ErrorType(err)
	}
	return slog.Group("",
		slog.String("errorType", typ),
		slog.String("errorMessage", fmt.Sprint(recovered)),
		slog.Any("stackTrace", StackFrames(stack)),
	)
}

// StackFrames converts a goroutine stack dump into one element per frame,
// "function (file:line)", starting at the frame that panicked.
func StackFrames(stack []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
	var frames []string
	for i := 0; i < len(lines); i++ {
		fn := strings.TrimSpace(lines[i])
		if fn == "" || strings.HasPrefix(fn, "goroutine ") {
			continue
		}
		if j := strings.LastIndex(fn, "("); j > 0 && strings.HasSuffix(fn, ")") {
			fn = fn[:j]
		}
		frame := fn
		if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") {
			loc := strings.TrimSpace(lines[i+1])
			if j := strings.LastIndex(loc, " +0x"); j >= 0 {
				loc = loc[:j]
			}
			frame = fn + " (" + loc + ")"
			i++
		}
		frames = append(frames, frame)
	}
	// Drop the frames of the stack capture and the panic machinery.
	for i, f := range frames {
		if strings.HasPrefix(f, "panic ") || f == "panic" {
			frames = frames[i+1:]
			break
		}
	}
	if len(frames) > maxFrames {
		frames = frames[:maxFrames]
	}
	return frames
}
