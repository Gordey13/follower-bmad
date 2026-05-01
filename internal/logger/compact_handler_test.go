package logger

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

func TestCompactHandlerOutput(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("session.restored",
		"task_id", "abc123-def456-ghi789-38980",
		"status", "valid",
		"duration_ms", int64(9),
	)
	line := buf.String()
	buf.Reset()

	checks := []string{"INFO", "[session.restored]", "task:…89-38980", "status:valid", "+9ms"}
	for _, c := range checks {
		if !strings.Contains(line, c) {
			t.Errorf("expected %q in output: %s", c, line)
		}
	}
}

func TestDebugFiltered(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Debug("should.not.appear", "key", "value")
	if buf.Len() > 0 {
		t.Errorf("DEBUG should be filtered, got: %s", buf.String())
	}
}

func TestWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)

	h2 := h.WithAttrs([]slog.Attr{slog.String("account", "long-account-id-xyz12345")})
	l := slog.New(h2)
	l.Warn("account.check", "duration_ms", int64(42))

	line := buf.String()
	if !strings.Contains(line, "acc:…xyz12345") {
		t.Errorf("expected truncated account in: %s", line)
	}
	if !strings.Contains(line, "+42ms") {
		t.Errorf("expected duration in: %s", line)
	}
}

func TestWithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)

	h3 := h.WithGroup("mygroup")
	l := slog.New(h3)
	l.Error("something.failed", "error", "timeout")

	line := buf.String()
	if !strings.Contains(line, "ERROR") {
		t.Errorf("expected ERROR in: %s", line)
	}
	if !strings.Contains(line, "[something.failed]") {
		t.Errorf("expected message in: %s", line)
	}
	if !strings.Contains(line, "mygroup.error:timeout") {
		t.Errorf("expected group-prefixed attr %q in: %s", "mygroup.error:timeout", line)
	}
}

func TestTimeFormat(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("time.check")

	line := buf.String()
	timePart := strings.Fields(line)[0]
	if !regexp.MustCompile(`^\d{2}:\d{2}:\d{2}\.\d{3}$`).MatchString(timePart) {
		t.Errorf("time format: got %q expected HH:MM:SS.mmm", timePart)
	}
}

func TestInterfaceAssertion(t *testing.T) {
	var _ slog.Handler = (*CompactHandler)(nil)
}

func TestSpacing(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("test.msg", "key", "val")
	line := buf.String()

	// Should have "LEVEL  message" (two spaces)
	if !strings.Contains(line, "INFO  [test.msg]") {
		t.Errorf("expected double space after level: %q", line)
	}
	// Should have "message  key:val" (two spaces)
	if !strings.Contains(line, "[test.msg]  key:val") {
		t.Errorf("expected double space before attrs: %q", line)
	}
}

func TestNoDuration(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("no.duration", "key", "val")

	line := buf.String()
	if strings.Contains(line, "+") {
		t.Errorf("expected no duration pattern in output: %s", line)
	}
}

func TestAttemptAndErrorCodeSuppression(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("event.one", "attempt", 1, "error_code", "eligible")
	line := buf.String()
	if strings.Contains(line, "attempt:1") {
		t.Errorf("attempt:1 should be suppressed: %s", line)
	}
	if strings.Contains(line, "error_code:eligible") {
		t.Errorf("error_code:eligible should be suppressed: %s", line)
	}

	buf.Reset()
	l.Info("event.two", "attempt", 2, "error_code", "timeout")
	line = buf.String()
	if !strings.Contains(line, "attempt:2") {
		t.Errorf("attempt:2 should be present: %s", line)
	}
	if !strings.Contains(line, "error_code:timeout") {
		t.Errorf("error_code:timeout should be present: %s", line)
	}
}

func TestAccountIDAlias(t *testing.T) {
	var buf bytes.Buffer
	h := NewCompactHandler(&buf)
	l := slog.New(h)

	l.Info("account.alias", "account_id", "33e5fb53-e663-bcf2-6f08-0071383dd559")
	line := buf.String()
	if !strings.Contains(line, "acc:…383dd559") {
		t.Errorf("expected account_id alias formatting in: %s", line)
	}
}
