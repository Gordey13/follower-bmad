package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResultRepositoryUpsertAndGetByTaskAttempt(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	accountID := createTestAccount(t, pool, "result-account-01")
	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(pool)

	taskID := uuid.New()
	if _, err := taskRepository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-result-01",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, ok, err := taskRepository.ClaimNextQueued(context.Background(), "worker-result-upsert")
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	saved, err := resultRepository.Upsert(context.Background(), domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           claimed.AccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             domain.FollowFlowOutcomeCompleted,
		Verified:            true,
		VerificationSignal:  domain.FollowVerificationSignalFollowConfirmed,
		VerificationReason:  "ui follow state confirmed",
		ScreenshotObjectKey: "accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/artifacts/execution.json",
		},
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if saved.TaskID != claimed.ID {
		t.Fatalf("expected task id %s, got %s", claimed.ID.String(), saved.TaskID.String())
	}

	got, err := resultRepository.GetByTaskAttempt(context.Background(), claimed.ID, claimed.Attempt)
	if err != nil {
		t.Fatalf("GetByTaskAttempt() error = %v", err)
	}
	if got.VerificationSignal != domain.FollowVerificationSignalFollowConfirmed {
		t.Fatalf("expected verification signal %s, got %s", domain.FollowVerificationSignalFollowConfirmed, got.VerificationSignal)
	}
	if len(got.ArtifactObjectKeys) != 1 {
		t.Fatalf("expected one artifact key, got %d", len(got.ArtifactObjectKeys))
	}
}

func TestResultRepositoryGetByTaskAttemptReturnsNotFound(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	resultRepository := postgresrepo.NewResultRepository(pool)
	_, err := resultRepository.GetByTaskAttempt(context.Background(), uuid.New(), 1)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowResultNotFound) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowResultNotFound, err)
	}
}

func TestResultRepositoryUpsertIsIdempotentByTaskAttempt(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	accountID := createTestAccount(t, pool, "result-account-02")
	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(pool)

	taskID := uuid.New()
	if _, err := taskRepository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-result-02",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, ok, err := taskRepository.ClaimNextQueued(context.Background(), "worker-result-idempotent")
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	_, err = resultRepository.Upsert(context.Background(), domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           claimed.AccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             domain.FollowFlowOutcomeNavigationFailed,
		Verified:            false,
		VerificationSignal:  domain.FollowVerificationSignalNavigationFailed,
		VerificationReason:  "navigation timeout",
		ErrorCode:           domain.ErrorCodeFollowNavigationFailed,
		ScreenshotObjectKey: "accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/artifacts/execution.json",
		},
	})
	if err != nil {
		t.Fatalf("first Upsert() error = %v", err)
	}

	updated, err := resultRepository.Upsert(context.Background(), domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           claimed.AccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             domain.FollowFlowOutcomeCompleted,
		Verified:            true,
		VerificationSignal:  domain.FollowVerificationSignalFollowConfirmed,
		VerificationReason:  "verified on retry",
		ScreenshotObjectKey: "accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/artifacts/execution-updated.json",
		},
	})
	if err != nil {
		t.Fatalf("second Upsert() error = %v", err)
	}
	if !updated.Verified {
		t.Fatal("expected updated record to be verified")
	}
	if updated.VerificationSignal != domain.FollowVerificationSignalFollowConfirmed {
		t.Fatalf("expected signal %s, got %s", domain.FollowVerificationSignalFollowConfirmed, updated.VerificationSignal)
	}
}

func TestResultRepositoryUpsertSucceedsWhenAuditFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	accountID := createTestAccount(t, pool, "result-account-03")
	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(
		pool,
		newFailingAuditLog(errors.New("audit store unavailable")),
	)

	taskID := uuid.New()
	if _, err := taskRepository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-result-03",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, ok, err := taskRepository.ClaimNextQueued(context.Background(), "worker-result-audit")
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	_, err = resultRepository.Upsert(context.Background(), domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           claimed.AccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             domain.FollowFlowOutcomeAlreadyDone,
		Verified:            true,
		VerificationSignal:  domain.FollowVerificationSignalAlreadyDone,
		VerificationReason:  "already in target state",
		ScreenshotObjectKey: "accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + claimed.AccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/artifacts/execution.json",
		},
	})
	if err != nil {
		t.Fatalf("Upsert() must succeed when audit fails, got error = %v", err)
	}
}

func TestResultRepositoryUpsertRejectsTaskAccountMismatch(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareFollowResultsSchema(t, pool)

	accountID := createTestAccount(t, pool, "result-account-owner")
	otherAccountID := createTestAccount(t, pool, "result-account-other")
	taskRepository := postgresrepo.NewTaskRepository(pool)
	resultRepository := postgresrepo.NewResultRepository(pool)

	taskID := uuid.New()
	if _, err := taskRepository.Enqueue(context.Background(), domain.Task{
		ID:            taskID,
		AccountID:     accountID,
		TargetProfile: "target-profile-mismatch",
		Status:        domain.TaskStatusQueued,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, ok, err := taskRepository.ClaimNextQueued(context.Background(), "worker-result-mismatch")
	if err != nil || !ok {
		t.Fatalf("ClaimNextQueued() expected success, got ok=%v err=%v", ok, err)
	}

	_, err = resultRepository.Upsert(context.Background(), domain.FollowResult{
		TaskID:              claimed.ID,
		AccountID:           otherAccountID,
		TargetProfile:       claimed.TargetProfile,
		Attempt:             claimed.Attempt,
		Outcome:             domain.FollowFlowOutcomeCompleted,
		Verified:            true,
		VerificationSignal:  domain.FollowVerificationSignalFollowConfirmed,
		VerificationReason:  "ui follow state confirmed",
		ScreenshotObjectKey: "accounts/" + otherAccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/screenshots/follow.png",
		ArtifactObjectKeys: []string{
			"accounts/" + otherAccountID.String() + "/tasks/" + claimed.ID.String() + "/attempts/1/artifacts/execution.json",
		},
	})
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowResultPersistFailed) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowResultPersistFailed, err)
	}
}

func prepareFollowResultsSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_id_account
			ON tasks (id, account_id);

		CREATE TABLE IF NOT EXISTS follow_results (
			task_id UUID NOT NULL,
			attempt INTEGER NOT NULL CHECK (attempt > 0),
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
			target_profile TEXT NOT NULL,
			outcome TEXT NOT NULL,
			verified BOOLEAN NOT NULL,
			verification_signal TEXT NOT NULL,
			verification_reason TEXT NULL,
			error_code TEXT NULL,
			screenshot_object_key TEXT NOT NULL,
			artifact_object_keys TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
			session_revision BIGINT NULL CHECK (session_revision > 0),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT pk_follow_results PRIMARY KEY (task_id, attempt),
			CONSTRAINT fk_follow_results_task_account FOREIGN KEY (task_id, account_id)
				REFERENCES tasks(id, account_id) ON DELETE CASCADE,
			CONSTRAINT chk_follow_results_target_profile_nonempty CHECK (
				NULLIF(BTRIM(target_profile), '') IS NOT NULL
			),
			CONSTRAINT chk_follow_results_outcome CHECK (
				outcome IN (
					'follow_completed',
					'follow_already_done',
					'follow_action_unavailable',
					'follow_target_unreachable',
					'follow_navigation_failed'
				)
			),
			CONSTRAINT chk_follow_results_signal CHECK (
				verification_signal IN (
					'follow_confirmed',
					'follow_already_done',
					'follow_action_unavailable',
					'follow_target_unreachable',
					'follow_navigation_failed',
					'follow_verify_failed'
				)
			),
			CONSTRAINT chk_follow_results_verified_vs_error CHECK (
				(verified = TRUE AND error_code IS NULL)
				OR (verified = FALSE AND NULLIF(BTRIM(COALESCE(error_code, '')), '') IS NOT NULL)
			),
			CONSTRAINT chk_follow_results_screenshot_nonempty CHECK (
				NULLIF(BTRIM(screenshot_object_key), '') IS NOT NULL
			),
			CONSTRAINT chk_follow_results_artifacts_nonempty CHECK (
				CARDINALITY(artifact_object_keys) > 0
			)
		);
	`)
	if err != nil {
		t.Fatalf("prepare follow_results schema: %v", err)
	}
}
