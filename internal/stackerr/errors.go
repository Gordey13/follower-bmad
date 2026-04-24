package stackerr

import (
    "errors"
    "log/slog"
    "runtime"
    "strings"
)

const defaultMaxStackDepth = 32

type Frame struct {
    Function string `json:"function"`
    File     string `json:"file"`
    Line     int    `json:"line"`
}

type Error struct {
    msg   string
    cause error
    stack []Frame
}

func New(msg string) error {
    return &Error{msg: strings.TrimSpace(msg), stack: captureStack(defaultMaxStackDepth, 2)}
}

func Wrap(err error, msg string) error {
    if err == nil {
        return New(msg)
    }

    stack := stackOf(err)
    if len(stack) == 0 {
        stack = captureStack(defaultMaxStackDepth, 2)
    }

    return &Error{msg: strings.TrimSpace(msg), cause: err, stack: stack}
}

func WithStack(err error) error {
    if err == nil {
        return nil
    }
    if len(stackOf(err)) > 0 {
        return err
    }
    return &Error{msg: "", cause: err, stack: captureStack(defaultMaxStackDepth, 2)}
}

func (e *Error) Error() string {
    if e == nil {
        return ""
    }
    msg := strings.TrimSpace(e.msg)
    switch {
    case msg == "" && e.cause != nil:
        return e.cause.Error()
    case msg == "":
        return "error"
    case e.cause == nil:
        return msg
    default:
        return msg + ": " + e.cause.Error()
    }
}

func (e *Error) Unwrap() error { if e==nil { return nil }; return e.cause }

func (e *Error) Is(target error) bool {
    if e == nil {
        return false
    }
    if target == e {
        return true
    }
    return errors.Is(e.cause, target)
}

func (e *Error) As(target any) bool {
    if e == nil {
        return false
    }
    if ptr, ok := target.(**Error); ok {
        *ptr = e
        return true
    }
    return errors.As(e.cause, target)
}

func (e *Error) LogValue() slog.Value {
	if e == nil {
		return slog.GroupValue(slog.String("msg", ""), slog.Any("stack_trace", []Frame{}))
	}
	attrs := []slog.Attr{slog.String("msg", e.messageForLog())}
	attrs = append(attrs, slog.Any("stack_trace", e.stack))
	return slog.GroupValue(attrs...)
}

func (e *Error) messageForLog() string {
	msg := strings.TrimSpace(e.msg)
	if msg != "" {
		return msg
	}
	return "error"
}

func (e *Error) StackTrace() []Frame {
    if e == nil || len(e.stack) == 0 {
        return nil
    }
    out := make([]Frame, len(e.stack))
    copy(out, e.stack)
    return out
}

func stackOf(err error) []Frame {
    var se *Error
    if errors.As(err, &se) && len(se.stack) > 0 {
        return se.stack
    }
    return nil
}

func captureStack(limit, skip int) []Frame {
    if limit <= 0 {
        return nil
    }

    pcs := make([]uintptr, limit+skip+4)
    n := runtime.Callers(skip+2, pcs)
    if n == 0 {
        return nil
    }

    frames := runtime.CallersFrames(pcs[:n])
    out := make([]Frame, 0, limit)
    for {
        frame, more := frames.Next()
        if !skipFrame(frame) {
            out = append(out, Frame{Function: frame.Function, File: frame.File, Line: frame.Line})
            if len(out) >= limit {
                break
            }
        }
        if !more {
            break
        }
    }
    return out
}

func skipFrame(frame runtime.Frame) bool {
    fn := frame.Function
    if strings.HasPrefix(fn, "runtime.") {
        return true
    }
    return strings.Contains(fn, "/internal/stackerr.") || strings.Contains(fn, "/internal/stackerr/")
}
