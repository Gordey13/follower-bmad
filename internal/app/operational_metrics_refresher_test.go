package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"follower/internal/domain"
	"follower/internal/observability"

	dto "github.com/prometheus/client_model/go"
)

type stubTaskSnapshotRepository struct {
	snapshot map[domain.TaskStatus]int64
	err      error
	respectContext bool
}

func (s stubTaskSnapshotRepository) TaskQueueSnapshot(ctx context.Context) (map[domain.TaskStatus]int64, error) {
	if s.respectContext {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

type stubAccountSnapshotRepository struct {
	snapshot map[domain.AccountOperationalState]int64
	err      error
	respectContext bool
}

func (s stubAccountSnapshotRepository) OperationalStateSnapshot(ctx context.Context) (map[domain.AccountOperationalState]int64, error) {
	if s.respectContext {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

type stubSessionSnapshotRepository struct {
	snapshot map[domain.SessionStatus]int64
	err      error
	respectContext bool
}

func (s stubSessionSnapshotRepository) StatusSnapshot(ctx context.Context) (map[domain.SessionStatus]int64, error) {
	if s.respectContext {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

func TestOperationalMetricsRefresherRefreshOnceUpdatesSnapshots(t *testing.T) {
	t.Parallel()

	registry := observability.NewMetricsRegistry()
	metrics := observability.NewTaskLifecycleMetrics(registry)
	refresher := newOperationalMetricsRefresher(
		stubTaskSnapshotRepository{snapshot: map[domain.TaskStatus]int64{
			domain.TaskStatusQueued:  4,
			domain.TaskStatusRunning: 2,
		}},
		stubAccountSnapshotRepository{snapshot: map[domain.AccountOperationalState]int64{
			domain.AccountStateActive: 5,
		}},
		stubSessionSnapshotRepository{snapshot: map[domain.SessionStatus]int64{
			domain.SessionStatusValid: 3,
		}},
		metrics,
		15*time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	if err := refresher.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce() error = %v", err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("registry.Gather() error = %v", err)
	}

	if value := gaugeValueByLabels(t, families, "follower_task_queue_total", map[string]string{"status": string(domain.TaskStatusQueued)}); value != 4 {
		t.Fatalf("expected queued gauge=4, got %v", value)
	}
	if value := gaugeValueByLabels(t, families, "follower_account_operational_total", map[string]string{"state": string(domain.AccountStateActive)}); value != 5 {
		t.Fatalf("expected account active gauge=5, got %v", value)
	}
	if value := gaugeValueByLabels(t, families, "follower_session_status_total", map[string]string{"status": string(domain.SessionStatusValid)}); value != 3 {
		t.Fatalf("expected session valid gauge=3, got %v", value)
	}
}

func TestOperationalMetricsRefresherRefreshOnceIsBestEffortOnFailure(t *testing.T) {
	t.Parallel()

	registry := observability.NewMetricsRegistry()
	metrics := observability.NewTaskLifecycleMetrics(registry)
	refresher := newOperationalMetricsRefresher(
		stubTaskSnapshotRepository{err: domain.NewDomainError(domain.ErrorCodeFollowResultNotFound, "no rows")},
		stubAccountSnapshotRepository{snapshot: map[domain.AccountOperationalState]int64{
			domain.AccountStateRestricted: 2,
		}},
		stubSessionSnapshotRepository{snapshot: map[domain.SessionStatus]int64{
			domain.SessionStatusUnavailable: 1,
		}},
		metrics,
		15*time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	err := refresher.RefreshOnce(context.Background())
	if err == nil {
		t.Fatal("expected refresh error when task snapshot fails")
	}
	if !errors.Is(err, domain.NewDomainError(domain.ErrorCodeFollowResultNotFound, "no rows")) &&
		!domain.IsDomainErrorCode(err, domain.ErrorCodeFollowResultNotFound) {
		t.Fatalf("expected domain error %s, got %v", domain.ErrorCodeFollowResultNotFound, err)
	}

	families, gatherErr := registry.Gather()
	if gatherErr != nil {
		t.Fatalf("registry.Gather() error = %v", gatherErr)
	}

	if value := gaugeValueByLabels(t, families, "follower_account_operational_total", map[string]string{"state": string(domain.AccountStateRestricted)}); value != 2 {
		t.Fatalf("expected account restricted gauge=2, got %v", value)
	}
	if value := gaugeValueByLabels(t, families, "follower_session_status_total", map[string]string{"status": string(domain.SessionStatusUnavailable)}); value != 1 {
		t.Fatalf("expected session unavailable gauge=1, got %v", value)
	}
	if value := counterValueByLabels(t, families, "follower_task_error_total", map[string]string{"stage": "refresh"}); value != 1 {
		t.Fatalf("expected refresh error counter=1, got %v", value)
	}
	if value := counterValueByLabels(
		t,
		families,
		"follower_task_error_code_total",
		map[string]string{"stage": "refresh", "error_code": string(domain.ErrorCodeFollowResultNotFound)},
	); value != 1 {
		t.Fatalf("expected refresh error_code counter=1, got %v", value)
	}
}

func TestOperationalMetricsRefresherRefreshOnceHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	registry := observability.NewMetricsRegistry()
	metrics := observability.NewTaskLifecycleMetrics(registry)
	refresher := newOperationalMetricsRefresher(
		stubTaskSnapshotRepository{respectContext: true},
		stubAccountSnapshotRepository{respectContext: true},
		stubSessionSnapshotRepository{respectContext: true},
		metrics,
		15*time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := refresher.RefreshOnce(ctx)
	if err == nil {
		t.Fatal("expected refresh cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	families, gatherErr := registry.Gather()
	if gatherErr != nil {
		t.Fatalf("registry.Gather() error = %v", gatherErr)
	}

	if value, found := counterValueByLabelsIfPresent(
		families,
		"follower_task_error_total",
		map[string]string{"stage": "refresh"},
	); found {
		t.Fatalf("expected no refresh error metric on context cancellation, got %v", value)
	}
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

func counterValueByLabelsIfPresent(
	families []*dto.MetricFamily,
	familyName string,
	labels map[string]string,
) (float64, bool) {
	for _, family := range families {
		if family.GetName() != familyName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if labelsMatch(metric.GetLabel(), labels) {
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}
