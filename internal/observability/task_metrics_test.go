package observability

import (
	"strings"
	"testing"

	"follower/internal/domain"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestTaskLifecycleMetricsExposePrometheusSeries(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	metrics := NewTaskLifecycleMetrics(registry)

	metrics.RecordClaimed()
	metrics.RecordStarted()
	metrics.RecordCompleted("success")
	metrics.RecordCompleted("retry")
	metrics.RecordError("claim")
	metrics.RecordError("complete")
	metrics.RecordErrorCode("claim", string(domain.ErrorCodeInternal))
	metrics.RecordExecutionOutcome("follow_completed")
	metrics.RecordDependencyReady("postgres", true)
	metrics.SetTaskQueueSnapshot(map[domain.TaskStatus]int64{
		domain.TaskStatusQueued:  2,
		domain.TaskStatusRunning: 1,
	})
	metrics.SetAccountOperationalSnapshot(map[domain.AccountOperationalState]int64{
		domain.AccountStateActive: 3,
	})
	metrics.SetSessionStatusSnapshot(map[domain.SessionStatus]int64{
		domain.SessionStatusValid: 2,
	})

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	names := make([]string, 0, len(families))
	for _, family := range families {
		names = append(names, family.GetName())
	}
	joined := strings.Join(names, ",")

	if !strings.Contains(joined, "follower_task_claimed_total") {
		t.Fatalf("expected follower_task_claimed_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_task_started_total") {
		t.Fatalf("expected follower_task_started_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_task_completed_total") {
		t.Fatalf("expected follower_task_completed_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_task_error_total") {
		t.Fatalf("expected follower_task_error_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_task_error_code_total") {
		t.Fatalf("expected follower_task_error_code_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_task_queue_total") {
		t.Fatalf("expected follower_task_queue_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_execution_outcome_total") {
		t.Fatalf("expected follower_execution_outcome_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_dependency_ready") {
		t.Fatalf("expected follower_dependency_ready in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_account_operational_total") {
		t.Fatalf("expected follower_account_operational_total in gathered metrics, got: %s", joined)
	}
	if !strings.Contains(joined, "follower_session_status_total") {
		t.Fatalf("expected follower_session_status_total in gathered metrics, got: %s", joined)
	}
}

func TestTaskLifecycleMetricsAppliesBoundedLabelPolicy(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	metrics := NewTaskLifecycleMetrics(registry)

	metrics.RecordCompleted("task-550e8400-e29b-41d4-a716-446655440000")
	metrics.RecordError("task/550e8400-e29b-41d4-a716-446655440000")
	metrics.RecordErrorCode("stage/with-cardinality", "account_id=550e8400-e29b-41d4-a716-446655440000")
	metrics.RecordExecutionOutcome("follow_completed_for_target_john_snow")
	metrics.RecordDependencyReady("postgres://user:password@host/db", true)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if value := counterValueByLabels(
		t,
		families,
		"follower_task_completed_total",
		map[string]string{"status": "unknown"},
	); value != 1 {
		t.Fatalf("expected status normalization to unknown, got %v", value)
	}
	if value := counterValueByLabels(
		t,
		families,
		"follower_task_error_total",
		map[string]string{"stage": "unknown"},
	); value != 1 {
		t.Fatalf("expected stage normalization to unknown, got %v", value)
	}
	if value := counterValueByLabels(
		t,
		families,
		"follower_task_error_code_total",
		map[string]string{"stage": "unknown", "error_code": string(domain.ErrorCodeInternal)},
	); value != 1 {
		t.Fatalf("expected stage/error_code normalization, got %v", value)
	}
	if value := counterValueByLabels(
		t,
		families,
		"follower_execution_outcome_total",
		map[string]string{"outcome": "unknown"},
	); value != 1 {
		t.Fatalf("expected outcome normalization to unknown, got %v", value)
	}
	if value := gaugeValueByLabels(
		t,
		families,
		"follower_dependency_ready",
		map[string]string{"dependency": "unknown"},
	); value != 1 {
		t.Fatalf("expected dependency normalization to unknown, got %v", value)
	}
}

func counterValueByLabels(
	t *testing.T,
	families []*dto.MetricFamily,
	familyName string,
	labels map[string]string,
) float64 {
	t.Helper()

	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metric.GetLabel(), labels) {
				return metric.GetCounter().GetValue()
			}
		}
	}

	t.Fatalf("metric family %s with labels %+v not found", familyName, labels)
	return 0
}

func gaugeValueByLabels(
	t *testing.T,
	families []*dto.MetricFamily,
	familyName string,
	labels map[string]string,
) float64 {
	t.Helper()

	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metric.GetLabel(), labels) {
				return metric.GetGauge().GetValue()
			}
		}
	}

	t.Fatalf("metric family %s with labels %+v not found", familyName, labels)
	return 0
}

func labelsMatch(pairs []*dto.LabelPair, want map[string]string) bool {
	if len(pairs) != len(want) {
		return false
	}

	for _, pair := range pairs {
		value, ok := want[pair.GetName()]
		if !ok || pair.GetValue() != value {
			return false
		}
	}
	return true
}
