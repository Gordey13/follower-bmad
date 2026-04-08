package browser

import (
	"context"
	"errors"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

func TestMockFollowFlowRunnerRequiresTargetProfile(t *testing.T) {
	t.Parallel()

	runner := NewMockFollowFlowRunner(nil, nil, nil)
	_, diagnostics, err := runner.RunFollowFlow(context.Background(), testFollowFlowInput(""))
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowTargetProfile, err)
	}
	if diagnostics.WarmupCompleted {
		t.Fatal("expected warmup to be marked as not completed on validation failure")
	}
}

func TestMockFollowFlowRunnerReturnsConfiguredOutcomes(t *testing.T) {
	t.Parallel()

	runner := NewMockFollowFlowRunner(
		map[domain.TargetProfileDescriptor]domain.FollowFlowOutcome{
			"already":     domain.FollowFlowOutcomeAlreadyDone,
			"unavailable": domain.FollowFlowOutcomeActionUnavailable,
		},
		nil,
		nil,
	)

	outcome, diagnostics, err := runner.RunFollowFlow(context.Background(), testFollowFlowInput("already"))
	if err != nil {
		t.Fatalf("RunFollowFlow(already) error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeAlreadyDone {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeAlreadyDone, outcome)
	}
	if diagnostics.Engine != "mock" {
		t.Fatalf("expected engine mock, got %s", diagnostics.Engine)
	}
	if !diagnostics.WarmupCompleted {
		t.Fatal("expected warmup to be marked as completed")
	}

	outcome, diagnostics, err = runner.RunFollowFlow(context.Background(), testFollowFlowInput("unavailable"))
	if err != nil {
		t.Fatalf("RunFollowFlow(unavailable) error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeActionUnavailable {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeActionUnavailable, outcome)
	}
	if diagnostics.Engine != "mock" {
		t.Fatalf("expected engine mock, got %s", diagnostics.Engine)
	}
	if !diagnostics.WarmupCompleted {
		t.Fatal("expected warmup to be marked as completed")
	}
}

func TestMockFollowFlowRunnerReturnsConfiguredError(t *testing.T) {
	t.Parallel()

	expectedErr := domain.NewDomainError(domain.ErrorCodeInternal, "transient browser dependency")
	runner := NewMockFollowFlowRunner(
		nil,
		map[domain.TargetProfileDescriptor]error{
			"retry-me": expectedErr,
		},
		nil,
	)

	_, _, err := runner.RunFollowFlow(context.Background(), testFollowFlowInput("retry-me"))
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected configured error, got %v", err)
	}
}

func TestNewFollowFlowRunnerRejectsUnsupportedEngine(t *testing.T) {
	t.Parallel()

	_, err := NewFollowFlowRunner("selenium", nil)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidOperationalState) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidOperationalState, err)
	}
}

func TestPlaywrightFollowFlowRunnerImplementsContract(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightFollowFlowRunner(nil, &stubPlaywrightFollowAdapter{
		outcome: domain.FollowFlowOutcomeCompleted,
	})
	outcome, diagnostics, err := runner.RunFollowFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100000"),
	)
	if err != nil {
		t.Fatalf("RunFollowFlow() error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeCompleted {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeCompleted, outcome)
	}
	if diagnostics.Engine != "playwright" {
		t.Fatalf("expected engine playwright, got %s", diagnostics.Engine)
	}
}

func TestPlaywrightFollowFlowRunnerReturnsAlreadyDoneOutcome(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightFollowFlowRunner(nil, &stubPlaywrightFollowAdapter{
		outcome: domain.FollowFlowOutcomeAlreadyDone,
	})
	outcome, _, err := runner.RunFollowFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100001"),
	)
	if err != nil {
		t.Fatalf("RunFollowFlow() error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeAlreadyDone {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeAlreadyDone, outcome)
	}
}

func TestPlaywrightFollowFlowRunnerReturnsActionUnavailableOutcome(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightFollowFlowRunner(nil, &stubPlaywrightFollowAdapter{
		outcome: domain.FollowFlowOutcomeActionUnavailable,
	})
	outcome, _, err := runner.RunFollowFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100002"),
	)
	if err != nil {
		t.Fatalf("RunFollowFlow() error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeActionUnavailable {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeActionUnavailable, outcome)
	}
}

func TestPlaywrightFollowFlowRunnerReturnsTargetUnreachableOutcome(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightFollowFlowRunner(nil, &stubPlaywrightFollowAdapter{
		outcome: domain.FollowFlowOutcomeTargetUnreachable,
	})
	outcome, _, err := runner.RunFollowFlow(
		context.Background(),
		testFollowFlowInput("https://oskelly.ru/profile/100003"),
	)
	if err != nil {
		t.Fatalf("RunFollowFlow() error = %v", err)
	}
	if outcome != domain.FollowFlowOutcomeTargetUnreachable {
		t.Fatalf("expected outcome %s, got %s", domain.FollowFlowOutcomeTargetUnreachable, outcome)
	}
}

func TestPlaywrightFollowFlowRunnerRejectsInvalidOskellyTargetProfile(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightFollowFlowRunner(nil, &stubPlaywrightFollowAdapter{
		outcome: domain.FollowFlowOutcomeCompleted,
	})
	_, _, err := runner.RunFollowFlow(
		context.Background(),
		testFollowFlowInput("target-without-oskelly-url"),
	)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeFollowTargetProfile) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeFollowTargetProfile, err)
	}
}

func testFollowFlowInput(target domain.TargetProfileDescriptor) domain.FollowFlowInput {
	accountID := uuid.New()
	return domain.FollowFlowInput{
		TaskID:             uuid.New(),
		AccountID:          accountID,
		Attempt:            1,
		ExecutionContextID: "worker-test-follow",
		SessionMetadata: domain.SessionMetadata{
			AccountID: accountID,
			Revision:  1,
			Status:    domain.SessionStatusValid,
			ObjectKey: "accounts/" + accountID.String() + "/sessions/1.json",
		},
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		TargetProfile:  target,
	}
}

type stubPlaywrightFollowAdapter struct {
	outcome   domain.FollowFlowOutcome
	warmupErr error
	followErr error
}

func (s *stubPlaywrightFollowAdapter) Warmup(
	ctx context.Context,
	input domain.FollowFlowInput,
) error {
	if s.warmupErr != nil {
		return s.warmupErr
	}
	return nil
}

func (s *stubPlaywrightFollowAdapter) RunFollowAction(
	ctx context.Context,
	input domain.FollowFlowInput,
) (domain.FollowFlowOutcome, error) {
	if s.followErr != nil {
		return "", s.followErr
	}
	return s.outcome, nil
}
