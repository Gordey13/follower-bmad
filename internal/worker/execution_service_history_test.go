package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestGetFollowResultsHistoryDelegatesToRepositoryAndLogs(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	targetProfile := domain.TargetProfileDescriptor("target-history-01")
	startedAt := time.Now().UTC().Add(-time.Hour)
	finishedAt := startedAt.Add(10 * time.Minute)

	var gotQuery domain.FollowResultsHistoryQuery
	repository := &historyExecutionResultRepository{
		listFn: func(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error) {
			gotQuery = query
			return []domain.FollowResultHistoryEntry{
				{
					TaskID:             uuid.New(),
					AccountID:          accountID,
					TargetProfile:      targetProfile,
					Attempt:            1,
					TaskStatus:         domain.TaskStatusSuccess,
					FollowOutcome:      domain.FollowFlowOutcomeCompleted,
					Verified:           true,
					VerificationSignal: domain.FollowVerificationSignalFollowConfirmed,
					CreatedAt:          finishedAt,
					UpdatedAt:          finishedAt,
				},
			}, nil
		},
	}

	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		logger,
	).WithResultRepository(repository)

	entries, err := service.GetFollowResultsHistory(context.Background(), domain.FollowResultsHistoryQuery{
		AccountID:     accountID,
		TargetProfile: targetProfile,
		From:          &startedAt,
		To:            &finishedAt,
		Limit:         25,
		Offset:        0,
	})
	if err != nil {
		t.Fatalf("GetFollowResultsHistory() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %d", len(entries))
	}
	if gotQuery.AccountID != accountID {
		t.Fatalf("expected account id %s, got %s", accountID.String(), gotQuery.AccountID.String())
	}
	if gotQuery.TargetProfile != targetProfile {
		t.Fatalf("expected target profile %s, got %s", targetProfile, gotQuery.TargetProfile)
	}

	logOutput := buffer.String()
	if !strings.Contains(logOutput, "follow.history.read") {
		t.Fatalf("expected follow.history.read log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "result_count=1") {
		t.Fatalf("expected result_count=1 in log, got %q", logOutput)
	}
}

func TestGetFollowResultsHistoryReturnsRepositoryError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("history query failed")
	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	).WithResultRepository(&historyExecutionResultRepository{
		listFn: func(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error) {
			return nil, expectedErr
		},
	})

	_, err := service.GetFollowResultsHistory(context.Background(), domain.FollowResultsHistoryQuery{
		Limit: 10,
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestGetFollowResultsHistoryFailsWhenRepositoryMissing(t *testing.T) {
	t.Parallel()

	service := NewExecutionService(
		&mockExecutionContextGuard{},
		&mockExecutionSessionRestorer{},
		testExecutionLogger(),
	)

	_, err := service.GetFollowResultsHistory(context.Background(), domain.FollowResultsHistoryQuery{
		Limit: 10,
	})
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowResultPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowResultPersistFailed, err)
	}
}

type historyExecutionResultRepository struct {
	listFn func(ctx context.Context, query domain.FollowResultsHistoryQuery) ([]domain.FollowResultHistoryEntry, error)
}

func (m *historyExecutionResultRepository) Upsert(
	ctx context.Context,
	result domain.FollowResult,
) (domain.FollowResult, error) {
	return result, nil
}

func (m *historyExecutionResultRepository) ListHistory(
	ctx context.Context,
	query domain.FollowResultsHistoryQuery,
) ([]domain.FollowResultHistoryEntry, error) {
	if m.listFn != nil {
		return m.listFn(ctx, query)
	}
	return nil, nil
}
