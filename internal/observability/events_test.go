package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLifecycleAttrsIncludeRequiredFields(t *testing.T) {
	t.Parallel()

	attrs := LifecycleAttrs(LifecycleContext{
		Component: "worker.claim_loop",
		TaskID:    "task-1",
		AccountID: "account-1",
		Attempt:   3,
		ErrorCode: "eligible",
	})

	values := attrMap(attrs)
	requiredKeys := []string{"component", "task_id", "account_id", "attempt", "error_code", "duration_ms"}
	for _, key := range requiredKeys {
		if _, ok := values[key]; !ok {
			t.Fatalf("expected key %q in lifecycle attrs, got %+v", key, values)
		}
	}
}

func TestErrorLifecycleAttrsAddsDiagnosticMessage(t *testing.T) {
	t.Parallel()

	attrs := ErrorLifecycleAttrs(
		LifecycleContext{
			Component: "worker.execution_service",
			TaskID:    "task-1",
			AccountID: "account-1",
			Attempt:   1,
			ErrorCode: "internal_error",
		},
		"execution context preparation failed",
	)

	values := attrMap(attrs)
	got, ok := values["diagnostic_message"]
	if !ok {
		t.Fatalf("expected diagnostic_message in attrs, got %+v", values)
	}
	if got != "execution context preparation failed" {
		t.Fatalf("expected diagnostic message %q, got %q", "execution context preparation failed", got)
	}
}

func TestErrorLifecycleAttrsWithErrorKeepsFieldsAndAddsStructuredError(t *testing.T) {
	t.Parallel()

	attrs := ErrorLifecycleAttrsWithError(
		LifecycleContext{
			Component: "worker.execution_service",
			TaskID:    "task-1",
			AccountID: "account-1",
			Attempt:   1,
			ErrorCode: "internal_error",
		},
		errors.New("cannot connect with credentials=secret-value"),
		"connection failed",
	)

	values := attrMap(attrs)
	if got := values["error_code"]; got != "internal_error" {
		t.Fatalf("expected error_code internal_error, got %q", got)
	}
	if got := values["diagnostic_message"]; got != "connection failed" {
		t.Fatalf("expected diagnostic_message connection failed, got %q", got)
	}

	var hasErrorAttr bool
	for index := 0; index < len(attrs)-1; index += 2 {
		key, ok := attrs[index].(string)
		if !ok || key != "error" {
			continue
		}
		hasErrorAttr = true
		if _, ok := attrs[index+1].(error); !ok {
			t.Fatalf("expected error attribute to contain error value, got %T", attrs[index+1])
		}
	}
	if !hasErrorAttr {
		t.Fatal("expected error attribute in lifecycle attrs")
	}
}

func TestLifecycleAttrsDefaultsToInternalErrorCodeWhenEmpty(t *testing.T) {
	t.Parallel()

	attrs := LifecycleAttrs(LifecycleContext{
		Component: "worker.claim_loop",
		TaskID:    "task-1",
		AccountID: "account-1",
		Attempt:   1,
		ErrorCode: "",
	})

	values := attrMap(attrs)
	if got := values["error_code"]; got != "internal_error" {
		t.Fatalf("expected fallback error_code internal_error, got %q", got)
	}
}

func TestSanitizeDiagnosticMessageRedactsSensitiveContent(t *testing.T) {
	t.Parallel()

	message := "restore failed: credentials=super-secret"
	got := SanitizeDiagnosticMessage(message)
	if strings.Contains(got, "super-secret") {
		t.Fatalf("expected sanitized message, got %q", got)
	}
	if got == "" {
		t.Fatal("expected non-empty sanitized diagnostic message")
	}
}

func TestRestoreLifecycleContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := WithRestoreLifecycleContext(context.Background(), "task-ctx-1", 4)
	got := RestoreLifecycleContextFrom(ctx)
	if got.TaskID != "task-ctx-1" {
		t.Fatalf("expected task_id task-ctx-1, got %q", got.TaskID)
	}
	if got.Attempt != 4 {
		t.Fatalf("expected attempt 4, got %d", got.Attempt)
	}
}

func TestRestoreLifecycleContextDefaultsWithoutContext(t *testing.T) {
	t.Parallel()

	got := RestoreLifecycleContextFrom(context.Background())
	if got.TaskID != "n/a" {
		t.Fatalf("expected default task_id n/a, got %q", got.TaskID)
	}
	if got.Attempt != 0 {
		t.Fatalf("expected default attempt 0, got %d", got.Attempt)
	}
}

func TestAdminLifecycleAttrsIncludeRequiredFields(t *testing.T) {
	t.Parallel()

	attrs := AdminLifecycleAttrs(AdminLifecycleContext{
		CorrelationID:   "corr-1",
		AdminAction:     EventAdminRetryTask,
		TaskID:          "task-1",
		OperationResult: "success",
		ErrorCode:       "none",
		DurationMS:      9,
		HTTPRoute:       "/api/v1/tasks/{id}/retry",
		HTTPStatusCode:  200,
	})

	values := attrMap(attrs)
	requiredKeys := []string{
		"correlation_id",
		"admin.action",
		"task_id",
		"operation.result",
		"error_code",
		"duration_ms",
		"http.route",
		"http.status_code",
	}
	for _, key := range requiredKeys {
		if _, ok := values[key]; !ok {
			t.Fatalf("expected key %q in admin lifecycle attrs, got %+v", key, values)
		}
	}
}

func attrMap(attrs []any) map[string]string {
	mapped := make(map[string]string, len(attrs)/2)
	for index := 0; index < len(attrs)-1; index += 2 {
		key, ok := attrs[index].(string)
		if !ok {
			continue
		}
		mapped[key] = toString(attrs[index+1])
	}
	return mapped
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return "int"
	case int64:
		return "int64"
	default:
		return ""
	}
}
