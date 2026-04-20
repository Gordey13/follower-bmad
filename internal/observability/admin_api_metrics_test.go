package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAdminAPIMetricsExposePrometheusSeries(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	metrics := NewAdminAPIMetrics(registry)
	metrics.RecordRequest(
		"/api/v1/tasks/{id}/retry",
		"POST",
		200,
		"success",
		"none",
		250*time.Millisecond,
	)
	metrics.RecordRequest(
		"/api/v1/tasks/{id}/retry",
		"POST",
		409,
		"error",
		"task_state_conflict",
		310*time.Millisecond,
	)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if value := counterValueByLabels(
		t,
		families,
		"follower_admin_api_requests_total",
		map[string]string{
			"route":        "/api/v1/tasks/{id}/retry",
			"method":       "post",
			"status_class": "2xx",
			"outcome":      "success",
			"error_code":   "none",
		},
	); value != 1 {
		t.Fatalf("expected success requests_total=1, got %v", value)
	}

	if value := counterValueByLabels(
		t,
		families,
		"follower_admin_api_errors_total",
		map[string]string{
			"route":        "/api/v1/tasks/{id}/retry",
			"method":       "post",
			"status_class": "4xx",
			"outcome":      "error",
			"error_code":   "task_state_conflict",
		},
	); value != 1 {
		t.Fatalf("expected errors_total=1, got %v", value)
	}
}

func TestAdminAPIMetricsAppliesBoundedLabelPolicy(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	metrics := NewAdminAPIMetrics(registry)
	metrics.RecordRequest(
		"/api/v1/tasks/550e8400-e29b-41d4-a716-446655440000",
		"CUSTOM",
		777,
		"super_successful",
		"task_id=550e8400-e29b-41d4-a716-446655440000",
		100*time.Millisecond,
	)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if value := counterValueByLabels(
		t,
		families,
		"follower_admin_api_requests_total",
		map[string]string{
			"route":        "/api/v1/unknown",
			"method":       "unknown",
			"status_class": "unknown",
			"outcome":      "unknown",
			"error_code":   "unknown",
		},
	); value != 1 {
		t.Fatalf("expected normalized unknown labels, got %v", value)
	}
}
