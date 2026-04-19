package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteJSONRendersDeterministicIndentedPayload(t *testing.T) {
	t.Parallel()

	payload := struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{
		ID:     "task-1",
		Status: "queued",
	}

	var buf bytes.Buffer
	if err := WriteJSON(&buf, payload); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "\"id\": \"task-1\"") {
		t.Fatalf("expected json payload to contain id, got:\n%s", out)
	}
	if !strings.Contains(out, "\"status\": \"queued\"") {
		t.Fatalf("expected json payload to contain status, got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected JSON output to end with newline, got %q", out)
	}
}

func TestWriteJSONReturnsMarshalError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteJSON(&buf, make(chan int)); err == nil {
		t.Fatal("expected marshal error, got nil")
	}
}
