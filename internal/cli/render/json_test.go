package render

import (
	"bytes"
	"encoding/json"
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

func TestWriteErrorJSONIncludesCorrelationID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteErrorJSON(&buf, ErrorPayload{
		Code:          "RETRY_NOT_ALLOWED",
		Message:       "retry is not allowed for the current task status",
		CorrelationID: "corr-123",
	}); err != nil {
		t.Fatalf("WriteErrorJSON() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal error: %v", err)
	}
	errorValue, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %#v", payload["error"])
	}
	if errorValue["code"] != "RETRY_NOT_ALLOWED" {
		t.Fatalf("expected code RETRY_NOT_ALLOWED, got %#v", errorValue["code"])
	}
	metaValue, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta object, got %#v", payload["meta"])
	}
	if metaValue["correlation_id"] != "corr-123" {
		t.Fatalf("expected correlation_id corr-123, got %#v", metaValue["correlation_id"])
	}
}
