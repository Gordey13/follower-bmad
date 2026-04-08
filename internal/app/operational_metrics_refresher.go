package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"follower/internal/config"
	"follower/internal/domain"
	"follower/internal/observability"
)

type operationalTaskSnapshotRepository interface {
	TaskQueueSnapshot(ctx context.Context) (map[domain.TaskStatus]int64, error)
}

type operationalAccountSnapshotRepository interface {
	OperationalStateSnapshot(ctx context.Context) (map[domain.AccountOperationalState]int64, error)
}

type operationalSessionSnapshotRepository interface {
	StatusSnapshot(ctx context.Context) (map[domain.SessionStatus]int64, error)
}

type operationalMetricsRefresher struct {
	taskRepository    operationalTaskSnapshotRepository
	accountRepository operationalAccountSnapshotRepository
	sessionRepository operationalSessionSnapshotRepository
	metrics           *observability.TaskLifecycleMetrics
	interval          time.Duration
	logger            *slog.Logger
}

func newOperationalMetricsRefresher(
	taskRepository operationalTaskSnapshotRepository,
	accountRepository operationalAccountSnapshotRepository,
	sessionRepository operationalSessionSnapshotRepository,
	metrics *observability.TaskLifecycleMetrics,
	interval time.Duration,
	logger *slog.Logger,
) *operationalMetricsRefresher {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}

	return &operationalMetricsRefresher{
		taskRepository:    taskRepository,
		accountRepository: accountRepository,
		sessionRepository: sessionRepository,
		metrics:           metrics,
		interval:          interval,
		logger:            logger,
	}
}

func (r *operationalMetricsRefresher) Run(ctx context.Context) {
	if r == nil {
		return
	}

	if err := r.RefreshOnce(ctx); shouldLogRefreshError(ctx, err) {
		r.logger.Warn("operational metrics refresh failed", "component", "observability.operational_metrics", "error", err)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RefreshOnce(ctx); shouldLogRefreshError(ctx, err) {
				r.logger.Warn("operational metrics refresh failed", "component", "observability.operational_metrics", "error", err)
			}
		}
	}
}

func (r *operationalMetricsRefresher) RefreshOnce(ctx context.Context) error {
	if r == nil {
		return nil
	}

	refreshCtx, cancel := context.WithTimeout(ctx, r.queryTimeout())
	defer cancel()
	if err := refreshCtx.Err(); err != nil {
		return err
	}

	var errs []error

	if snapshot, err := r.taskRepository.TaskQueueSnapshot(refreshCtx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return ctxErr
		}
		errs = append(errs, fmt.Errorf("task queue snapshot: %w", err))
		r.recordRefreshError(err)
	} else {
		r.metrics.SetTaskQueueSnapshot(snapshot)
	}

	if snapshot, err := r.accountRepository.OperationalStateSnapshot(refreshCtx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return ctxErr
		}
		errs = append(errs, fmt.Errorf("account operational snapshot: %w", err))
		r.recordRefreshError(err)
	} else {
		r.metrics.SetAccountOperationalSnapshot(snapshot)
	}

	if snapshot, err := r.sessionRepository.StatusSnapshot(refreshCtx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return ctxErr
		}
		errs = append(errs, fmt.Errorf("session status snapshot: %w", err))
		r.recordRefreshError(err)
	} else {
		r.metrics.SetSessionStatusSnapshot(snapshot)
	}

	return errors.Join(errs...)
}

func (r *operationalMetricsRefresher) recordRefreshError(err error) {
	if r == nil || r.metrics == nil {
		return
	}

	errorCode := domain.ErrorCodeInternal
	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		errorCode = domainErr.Code
	}

	r.metrics.RecordError("refresh")
	r.metrics.RecordErrorCode("refresh", string(errorCode))
}

func (r *operationalMetricsRefresher) queryTimeout() time.Duration {
	timeout := r.interval / 2
	if timeout < time.Second {
		timeout = time.Second
	}
	if timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	return timeout
}

func operationalMetricsRefreshInterval(cfg config.Config) time.Duration {
	seconds := cfg.Worker.LoopIntervalSeconds
	if seconds <= 0 {
		seconds = 30
	}
	if seconds < 5 {
		seconds = 5
	}
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func shouldLogRefreshError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx == nil {
		return true
	}
	ctxErr := ctx.Err()
	if ctxErr != nil && errors.Is(err, ctxErr) {
		return false
	}
	return true
}
