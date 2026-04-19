package adminclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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

func TestClientRetryTaskParsesSuccessEnvelopeAndMeta(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/tasks/task-1/retry" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"source_task_id": "task-1",
				"new_task_id":    "task-2",
				"status":         "queued",
			},
			"error": nil,
			"meta": map[string]any{
				"correlation_id": "corr-retry-001",
			},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.RetryTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("RetryTask() error = %v", err)
	}
	if resp.SourceTaskID != "task-1" {
		t.Fatalf("expected source_task_id=task-1, got %q", resp.SourceTaskID)
	}
	if resp.NewTaskID != "task-2" {
		t.Fatalf("expected new_task_id=task-2, got %q", resp.NewTaskID)
	}
	if resp.CorrelationID != "corr-retry-001" {
		t.Fatalf("expected correlation_id=corr-retry-001, got %q", resp.CorrelationID)
	}
}

func TestClientCancelTaskMapsEnvelopeAPIErrorAndMeta(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/tasks/task-1/cancel" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": nil,
			"error": map[string]any{
				"code":    "CANCEL_NOT_ALLOWED",
				"message": "cancel is not allowed for the current task status",
			},
			"meta": map[string]any{
				"correlation_id": "corr-cancel-001",
			},
		})
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.CancelTask(context.Background(), "task-1")
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
	if clientErr.Code != "CANCEL_NOT_ALLOWED" {
		t.Fatalf("expected code CANCEL_NOT_ALLOWED, got %q", clientErr.Code)
	}
	if clientErr.CorrelationID != "corr-cancel-001" {
		t.Fatalf("expected correlation_id corr-cancel-001, got %q", clientErr.CorrelationID)
	}
}

func TestClientRetryTaskHandlesMalformedBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-json")
	}))
	defer server.Close()

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.RetryTask(context.Background(), "task-1")
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

func TestClientRetryTaskHandlesNon2xxWithoutAPIErrorPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"status": "ignored",
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

	_, err = client.RetryTask(context.Background(), "task-1")
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
	if clientErr.Code != "HTTP_502" {
		t.Fatalf("expected code HTTP_502, got %q", clientErr.Code)
	}
}

func TestClientCancelTaskHandlesNetworkFailure(t *testing.T) {
	t.Parallel()

	client, err := New(
		"http://example.invalid",
		&http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, &net.OpError{Op: "dial", Err: errors.New("dial timeout")}
			}),
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.CancelTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected network error, got nil")
	}

	var clientErr *Error
	if !errors.As(err, &clientErr) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if clientErr.Kind != ErrorKindNetwork {
		t.Fatalf("expected kind=%q, got %q", ErrorKindNetwork, clientErr.Kind)
	}
	if clientErr.Code != "NETWORK_ERROR" {
		t.Fatalf("expected code NETWORK_ERROR, got %q", clientErr.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
