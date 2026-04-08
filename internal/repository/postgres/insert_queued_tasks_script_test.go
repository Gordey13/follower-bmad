package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
)

const (
	queuedPolicyBlockStart  = "-- BMAD_EDIT_POLICY_START"
	queuedPolicyBlockEnd    = "-- BMAD_EDIT_POLICY_END"
	queuedTargetsBlockStart = "-- BMAD_EDIT_TARGETS_START"
	queuedTargetsBlockEnd   = "-- BMAD_EDIT_TARGETS_END"
)

func TestInsertQueuedTasksScriptInsertsValidOskellyProfilesAndClaimPath(t *testing.T) {
	pool := mustOpenTestPool(t)
	ctx := context.Background()

	accountRepo := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())
	accountID := uuid.New()
	if err := accountRepo.CreateAccount(ctx, domain.Account{
		ID:               accountID,
		Username:         "tasks-script-valid-nosession-noproxy-01",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	baseScript := mustReadProjectFile(t, "scripts", "insert-queued-tasks.sql")
	targets := strings.Join([]string{
		"'https://oskelly.ru/profile/100001'",
		"'https://oskelly.ru/profile/100002'",
	}, ",\n    ")
	sqlText := withQueuedTaskTargets(
		withQueuedTaskPolicy(baseScript, false, false),
		targets,
	)

	if _, err := pool.Exec(ctx, sqlText); err != nil {
		t.Fatalf("insert-queued-tasks.sql execution error = %v", err)
	}

	var insertedCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE account_id = $1
	`).Scan(&insertedCount); err != nil {
		t.Fatalf("count inserted tasks: %v", err)
	}
	if insertedCount != 2 {
		t.Fatalf("expected 2 inserted tasks, got %d", insertedCount)
	}

	var invalidPatternCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE account_id = $1
		  AND target_profile !~ '^https://oskelly\.ru/profile/[0-9]+$'
	`).Scan(&invalidPatternCount); err != nil {
		t.Fatalf("validate target_profile pattern: %v", err)
	}
	if invalidPatternCount != 0 {
		t.Fatalf("expected all inserted target profiles to match oskelly pattern, invalid rows=%d", invalidPatternCount)
	}

	taskRepo := postgresrepo.NewTaskRepository(pool)
	claimed, ok, err := taskRepo.ClaimNextQueued(ctx, "worker-script-claim-01")
	if err != nil {
		t.Fatalf("ClaimNextQueued() error = %v", err)
	}
	if !ok {
		t.Fatal("expected queued task from script to be claimable")
	}
	if claimed.Status != domain.TaskStatusRunning {
		t.Fatalf("expected status %s after claim, got %s", domain.TaskStatusRunning, claimed.Status)
	}
	if claimed.Attempt != 1 {
		t.Fatalf("expected attempt=1 after claim, got %d", claimed.Attempt)
	}
}

func TestInsertQueuedTasksScriptRejectsInvalidTargetProfile(t *testing.T) {
	pool := mustOpenTestPool(t)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "tasks-script-invalid-target-01")
	_ = accountID

	baseScript := mustReadProjectFile(t, "scripts", "insert-queued-tasks.sql")
	targets := strings.Join([]string{
		"'https://oskelly.ru/profile/100100'",
		"'https://oskelly.ru/profiles/not-valid'",
	}, ",\n    ")
	sqlText := withQueuedTaskTargets(
		withQueuedTaskPolicy(baseScript, false, false),
		targets,
	)

	_, err := pool.Exec(ctx, sqlText)
	if err == nil {
		t.Fatal("expected script execution to fail for invalid target_profile")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid target_profile format") {
		t.Fatalf("expected invalid target_profile error, got %v", err)
	}

	var taskCount int
	if scanErr := pool.QueryRow(ctx, `SELECT COUNT(*)::INT FROM tasks`).Scan(&taskCount); scanErr != nil {
		t.Fatalf("count tasks after failed script: %v", scanErr)
	}
	if taskCount != 0 {
		t.Fatalf("expected no task inserts after validation failure, got %d", taskCount)
	}
}

func TestInsertQueuedTasksScriptKeepsActiveQueueDedupeAndAllowsReenqueueAfterSuccess(t *testing.T) {
	pool := mustOpenTestPool(t)
	ctx := context.Background()

	accountID := createTestAccount(t, pool, "tasks-script-dedupe-01")
	baseScript := mustReadProjectFile(t, "scripts", "insert-queued-tasks.sql")
	target := "'https://oskelly.ru/profile/100777'"
	sqlText := withQueuedTaskTargets(
		withQueuedTaskPolicy(baseScript, false, false),
		target,
	)

	if _, err := pool.Exec(ctx, sqlText); err != nil {
		t.Fatalf("first script execution error = %v", err)
	}
	if _, err := pool.Exec(ctx, sqlText); err != nil {
		t.Fatalf("second script execution error = %v", err)
	}

	var activeCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE account_id = $1
		  AND target_profile = 'https://oskelly.ru/profile/100777'
		  AND status IN ('queued', 'running')
	`, accountID).Scan(&activeCount); err != nil {
		t.Fatalf("count active deduped tasks: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active deduped task, got %d", activeCount)
	}

	taskRepo := postgresrepo.NewTaskRepository(pool)
	claimed, ok, err := taskRepo.ClaimNextQueued(ctx, "worker-script-dedupe-01")
	if err != nil {
		t.Fatalf("ClaimNextQueued() error = %v", err)
	}
	if !ok {
		t.Fatal("expected queued task to be claimed for completion path")
	}
	if _, err := taskRepo.Complete(
		ctx,
		claimed.ID,
		"worker-script-dedupe-01",
		domain.TaskStatusSuccess,
		"",
		"",
	); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if _, err := pool.Exec(ctx, sqlText); err != nil {
		t.Fatalf("third script execution error = %v", err)
	}

	var totalCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM tasks
		WHERE account_id = $1
		  AND target_profile = 'https://oskelly.ru/profile/100777'
	`, accountID).Scan(&totalCount); err != nil {
		t.Fatalf("count all tasks after terminal re-enqueue: %v", err)
	}
	if totalCount != 2 {
		t.Fatalf("expected terminal status not to block re-enqueue, total rows=%d", totalCount)
	}
}

func withQueuedTaskPolicy(sqlText string, proxyRequired bool, validSessionRequired bool) string {
	return replaceEditableBlock(
		sqlText,
		queuedPolicyBlockStart,
		queuedPolicyBlockEnd,
		fmt.Sprintf(
			"SELECT %t::BOOLEAN AS proxy_binding_required, %t::BOOLEAN AS valid_session_required",
			proxyRequired,
			validSessionRequired,
		),
	)
}

func withQueuedTaskTargets(sqlText string, targets string) string {
	return replaceEditableBlock(sqlText, queuedTargetsBlockStart, queuedTargetsBlockEnd, targets)
}
