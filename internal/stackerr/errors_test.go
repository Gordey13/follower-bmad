package stackerr

import (
    "bytes"
    "encoding/json"
    "errors"
    "log/slog"
    "strings"
    "testing"
)

type markerErr struct{}

func (markerErr) Error() string { return "marker" }

func TestNewIncludesStackAndLogValue(t *testing.T) {
    t.Parallel()

    err := New("boom")
    se, ok := err.(*Error)
    if !ok {
        t.Fatalf("expected *Error, got %T", err)
    }
    if len(se.StackTrace()) == 0 {
        t.Fatal("expected non-empty stack trace")
    }

    var out bytes.Buffer
    logger := slog.New(slog.NewJSONHandler(&out, nil))
    logger.Error("operation failed", "error", err)

    var payload map[string]any
    if decodeErr := json.Unmarshal(out.Bytes(), &payload); decodeErr != nil {
        t.Fatalf("decode log json: %v", decodeErr)
    }

    errorField, ok := payload["error"].(map[string]any)
    if !ok {
        t.Fatalf("expected error object in log payload, got %#v", payload["error"])
    }
    if msg, _ := errorField["msg"].(string); msg != "boom" {
        t.Fatalf("expected msg=boom, got %q", msg)
    }
    frames, ok := errorField["stack_trace"].([]any)
    if !ok || len(frames) == 0 {
        t.Fatalf("expected stack_trace array, got %#v", errorField["stack_trace"])
    }
}

func TestWrapPreservesErrorsIsAndAs(t *testing.T) {
    t.Parallel()

    base := markerErr{}
    wrapped := Wrap(base, "context")

    if !errors.Is(wrapped, base) {
        t.Fatal("expected errors.Is to match base error")
    }

    var got markerErr
    if !errors.As(wrapped, &got) {
        t.Fatal("expected errors.As to match markerErr")
    }
}

func TestWithStackPreservesOriginalMessage(t *testing.T) {
    t.Parallel()

    base := errors.New("plain error")
    stacked := WithStack(base)

    if stacked == nil {
        t.Fatal("expected non-nil error")
    }
    if stacked.Error() != base.Error() {
        t.Fatalf("expected same error message, got %q", stacked.Error())
    }

    se, ok := stacked.(*Error)
    if !ok {
        t.Fatalf("expected *Error from WithStack, got %T", stacked)
    }
    if len(se.StackTrace()) == 0 {
        t.Fatal("expected stack trace from WithStack")
    }
}

func TestWrapDoesNotDuplicateExistingStack(t *testing.T) {
    t.Parallel()

    first := New("root")
    firstStack := mustStack(t, first)

    second := Wrap(first, "level-2")
    secondStack := mustStack(t, second)

    if len(firstStack) != len(secondStack) {
        t.Fatalf("expected reused stack length, got %d vs %d", len(firstStack), len(secondStack))
    }

    for i := range firstStack {
        if firstStack[i] != secondStack[i] {
            t.Fatalf("expected same stack frame at %d", i)
        }
    }
}

func TestCapturedStackSkipsRuntimeAndStackerrFrames(t *testing.T) {
    t.Parallel()

    err := New("test")
    stack := mustStack(t, err)

    for _, frame := range stack {
        if strings.HasPrefix(frame.Function, "runtime.") {
            t.Fatalf("unexpected runtime frame: %s", frame.Function)
        }
        if strings.Contains(frame.Function, "/internal/stackerr") || strings.Contains(frame.Function, "stackerr.") {
            t.Fatalf("unexpected stackerr frame: %s", frame.Function)
        }
    }
}

func mustStack(t *testing.T, err error) []Frame {
    t.Helper()

    se, ok := err.(*Error)
    if !ok {
        t.Fatalf("expected *Error, got %T", err)
    }
    stack := se.StackTrace()
    if len(stack) == 0 {
        t.Fatal("expected stack trace")
    }
    return stack
}
