package adminclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientListTasksParsesEnvelope(t *testing.T) {
	t.Parallel()

	taskUpdatedAt := time.Date(2026, 4, 19, 12, 30, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tasks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"tasks": []map[string]any{
					{
						"id":             "task-1",
						"account_id":     "account-1",
						"target_profile": "https://example.com/p/1",
						"status":         "queued",
						"attempt":        0,
						"error_code":     nil,
						"result_reason":  nil,
						"updated_at":     taskUpdatedAt.Format(time.RFC3339),
					},
				},
			},
			"error": nil,
			"meta":  map[string]any{},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(resp.Tasks))
	}
	if resp.Tasks[0].ID != "task-1" {
		t.Fatalf("expected id task-1, got %q", resp.Tasks[0].ID)
	}
	if !resp.Tasks[0].UpdatedAt.Equal(taskUpdatedAt) {
		t.Fatalf("expected updated_at %v, got %v", taskUpdatedAt, resp.Tasks[0].UpdatedAt)
	}
}

func TestClientGetTaskReturnsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": nil,
			"error": map[string]any{
				"code":    "TASK_NOT_FOUND",
				"message": "task not found",
			},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.GetTask(context.Background(), "task-unknown")
	if err == nil {
		t.Fatal("expected API error, got nil")
	}

	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if clientErr.Kind != ErrorKindAPI {
		t.Fatalf("expected kind=%q, got %q", ErrorKindAPI, clientErr.Kind)
	}
	if clientErr.Code != "TASK_NOT_FOUND" {
		t.Fatalf("expected code TASK_NOT_FOUND, got %q", clientErr.Code)
	}
	if clientErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, clientErr.StatusCode)
	}
}

func TestClientListFailuresHandlesMalformedEnvelope(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not-json"))
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.ListFailures(context.Background())
	if err == nil {
		t.Fatal("expected protocol error, got nil")
	}

	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if clientErr.Kind != ErrorKindProtocol {
		t.Fatalf("expected kind=%q, got %q", ErrorKindProtocol, clientErr.Kind)
	}
}
