package httptransport

import (
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"follower/internal/domain"
	"follower/internal/observability"
)

func TestMetricsEndpointPrometheusOutput(t *testing.T) {
	t.Parallel()

	registry := observability.NewMetricsRegistry()
	metrics := observability.NewTaskLifecycleMetrics(registry)
	metrics.RecordClaimed()
	metrics.RecordStarted()
	metrics.RecordCompleted(string(domain.TaskStatusSuccess))
	metrics.RecordError("claim")
	metrics.RecordErrorCode("claim", string(domain.ErrorCodeInternal))
	metrics.RecordExecutionOutcome("follow_completed")
	metrics.RecordDependencyReady("postgres", true)
	metrics.SetTaskQueueSnapshot(map[domain.TaskStatus]int64{
		domain.TaskStatusQueued: 3,
	})
	metrics.SetAccountOperationalSnapshot(map[domain.AccountOperationalState]int64{
		domain.AccountStateActive: 2,
	})
	metrics.SetSessionStatusSnapshot(map[domain.SessionStatus]int64{
		domain.SessionStatusValid: 2,
	})

	metricsHandler := NewMetricsHandler(
		observability.NewMetricsHandler(registry),
	)
	server := httptest.NewServer(NewServer(ServerConfig{Address: ":0"}, stdhttp.NotFoundHandler(), metricsHandler).Handler)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	output := string(body)
	if !strings.Contains(output, "# HELP go_gc_duration_seconds") {
		t.Fatalf("expected prometheus text format output, got: %s", output)
	}
	requiredSeries := []string{
		"follower_task_claimed_total",
		"follower_task_started_total",
		"follower_task_completed_total",
		"follower_task_error_total",
		"follower_task_error_code_total",
		"follower_task_queue_total",
		"follower_execution_outcome_total",
		"follower_dependency_ready",
		"follower_account_operational_total",
		"follower_session_status_total",
	}
	for _, series := range requiredSeries {
		if !strings.Contains(output, series) {
			t.Fatalf("expected %s in /metrics output, got: %s", series, output)
		}
	}
}
