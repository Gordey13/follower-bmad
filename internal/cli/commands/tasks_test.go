package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"follower/internal/cli/adminclient"
)

func TestExecuteTasksListDefaultsToTableOutput(t *testing.T) {
	t.Parallel()

	server := newReadCommandsTestServer(t)
	defer server.Close()

	client, err := adminclient.New(server.URL, nil)
	if err != nil {
		t.Fatalf("adminclient.New() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"tasks", "list"},
		Dependencies{
			Client: client,
			Stdout: &stdout,
			Stderr: &stderr,
		},
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"ID", "STATUS", "00000000-0000-0000-0000-000000000001", "queued"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected table output to contain %q, got:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr for success path, got %q", stderr.String())
	}
}

func TestExecuteTasksGetJSONOutput(t *testing.T) {
	t.Parallel()

	server := newReadCommandsTestServer(t)
	defer server.Close()

	client, err := adminclient.New(server.URL, nil)
	if err != nil {
		t.Fatalf("adminclient.New() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"tasks", "get", "--output", "json", "00000000-0000-0000-0000-000000000001"},
		Dependencies{
			Client: client,
			Stdout: &stdout,
			Stderr: &stderr,
		},
	)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if payload["id"] != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected id=00000000-0000-0000-0000-000000000001, got %v", payload["id"])
	}
	if payload["status"] != "queued" {
		t.Fatalf("expected status=queued, got %v", payload["status"])
	}
}

func TestExecuteTasksFailuresReturnsExitCodeOneOnAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": nil,
			"error": map[string]any{
				"code":    "ADMIN_ENDPOINT_NOT_AVAILABLE",
				"message": "admin endpoint is temporarily unavailable",
			},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()

	client, err := adminclient.New(server.URL, nil)
	if err != nil {
		t.Fatalf("adminclient.New() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"tasks", "failures"},
		Dependencies{
			Client: client,
			Stdout: &stdout,
			Stderr: &stderr,
		},
	)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "ADMIN_ENDPOINT_NOT_AVAILABLE") {
		t.Fatalf("expected stderr to contain API code, got %q", errOut)
	}
}

func TestExecuteOutputsJSONErrorWhenRequested(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": nil,
			"error": map[string]any{
				"code":    "ADMIN_ENDPOINT_NOT_AVAILABLE",
				"message": "admin endpoint is temporarily unavailable",
			},
			"meta": map[string]any{},
		})
	}))
	defer server.Close()

	client, err := adminclient.New(server.URL, nil)
	if err != nil {
		t.Fatalf("adminclient.New() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"tasks", "list", "--output", "json"},
		Dependencies{
			Client: client,
			Stdout: &stdout,
			Stderr: &stderr,
		},
	)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	var payload map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
		t.Fatalf("stderr is not valid JSON: %v", err)
	}
	errorValue, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %#v", payload["error"])
	}
	if errorValue["code"] == "" {
		t.Fatalf("expected non-empty error code, got %#v", errorValue["code"])
	}
}

func TestExecuteHandlesMalformedAPIEnvelope(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not-json"))
	}))
	defer server.Close()

	client, err := adminclient.New(server.URL, nil)
	if err != nil {
		t.Fatalf("adminclient.New() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		context.Background(),
		[]string{"tasks", "list"},
		Dependencies{
			Client: client,
			Stdout: &stdout,
			Stderr: &stderr,
		},
	)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if strings.TrimSpace(stderr.String()) == "" {
		t.Fatal("expected stable error output on malformed API response")
	}
}

func newReadCommandsTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	updatedAt := time.Date(2026, 4, 19, 13, 0, 0, 0, time.UTC)
	startedAt := updatedAt.Add(-2 * time.Minute)
	createdAt := updatedAt.Add(-5 * time.Minute)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{
						{
							"id":             "00000000-0000-0000-0000-000000000001",
							"account_id":     "11111111-1111-1111-1111-111111111111",
							"target_profile": "https://example.com/p/1",
							"status":         "queued",
							"attempt":        0,
							"error_code":     nil,
							"result_reason":  nil,
							"updated_at":     updatedAt.Format(time.RFC3339),
						},
					},
				},
				"error": nil,
				"meta":  map[string]any{},
			})
		case "/api/v1/tasks/00000000-0000-0000-0000-000000000001":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":             "00000000-0000-0000-0000-000000000001",
					"account_id":     "11111111-1111-1111-1111-111111111111",
					"target_profile": "https://example.com/p/1",
					"status":         "queued",
					"attempt":        0,
					"claimed_by":     nil,
					"claimed_at":     nil,
					"started_at":     startedAt.Format(time.RFC3339),
					"finished_at":    nil,
					"error_code":     nil,
					"result_reason":  nil,
					"created_at":     createdAt.Format(time.RFC3339),
					"updated_at":     updatedAt.Format(time.RFC3339),
					"attempt_context": map[string]any{
						"outcome":               "navigation_failed",
						"verified":              false,
						"verification_signal":   "navigation_failed",
						"verification_reason":   "timeout",
						"error_code":            "FOLLOW_NAVIGATION_FAILED",
						"screenshot_object_key": "screenshots/task-0001.png",
						"artifact_object_keys":  []string{"artifacts/task-0001.json"},
					},
				},
				"error": nil,
				"meta":  map[string]any{},
			})
		case "/api/v1/tasks/failures":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"tasks": []map[string]any{
						{
							"id":                  "00000000-0000-0000-0000-000000000002",
							"account_id":          "22222222-2222-2222-2222-222222222222",
							"target_profile":      "https://example.com/p/2",
							"status":              "fail",
							"attempt":             1,
							"error_code":          "FOLLOW_NAVIGATION_FAILED",
							"result_reason":       "timeout",
							"updated_at":          updatedAt.Format(time.RFC3339),
							"follow_outcome":      "navigation_failed",
							"verification_signal": "navigation_failed",
						},
					},
				},
				"error": nil,
				"meta":  map[string]any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}
