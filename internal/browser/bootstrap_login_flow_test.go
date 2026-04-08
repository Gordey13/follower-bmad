package browser

import (
	"context"
	"errors"
	"testing"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type mockBootstrapResolver struct {
	resolveFn func(
		ctx context.Context,
		source domain.CredentialSource,
		reference string,
	) (domain.AccountCredentials, error)
}

func (r *mockBootstrapResolver) Resolve(
	ctx context.Context,
	source domain.CredentialSource,
	reference string,
) (domain.AccountCredentials, error) {
	if r.resolveFn != nil {
		return r.resolveFn(ctx, source, reference)
	}
	return domain.AccountCredentials{
		Username: "user@example.com",
		Password: "secret",
	}, nil
}

type mockPlaywrightBootstrapAdapter struct {
	outcome domain.BootstrapLoginOutcome
	payload []byte
	err     error
}

func (a *mockPlaywrightBootstrapAdapter) Execute(
	ctx context.Context,
	credentials domain.AccountCredentials,
) (domain.BootstrapLoginOutcome, []byte, error) {
	return a.outcome, a.payload, a.err
}

func TestNewBootstrapLoginRunnerRejectsUnsupportedEngine(t *testing.T) {
	t.Parallel()

	_, err := NewBootstrapLoginRunner("selenium", &mockBootstrapResolver{}, nil)
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidOperationalState) {
		t.Fatalf("expected %s, got %v", domain.ErrorCodeInvalidOperationalState, err)
	}
}

func TestMockBootstrapLoginRunnerReturnsConfiguredOutcome(t *testing.T) {
	t.Parallel()

	runner := NewMockBootstrapLoginRunner(
		&mockBootstrapResolver{},
		map[string]domain.BootstrapLoginOutcome{
			"env://FOLLOWER_LOGIN_USER,FOLLOWER_LOGIN_PASSWORD": domain.BootstrapLoginOutcomeAuthChallenge,
		},
		nil,
	)

	result, err := runner.RunBootstrapLogin(context.Background(), testBootstrapLoginInput())
	if err != nil {
		t.Fatalf("RunBootstrapLogin() error = %v", err)
	}
	if result.Outcome != domain.BootstrapLoginOutcomeAuthChallenge {
		t.Fatalf("expected outcome %s, got %s", domain.BootstrapLoginOutcomeAuthChallenge, result.Outcome)
	}
	if result.Diagnostics.Engine != "mock" {
		t.Fatalf("expected engine mock, got %s", result.Diagnostics.Engine)
	}
}

func TestMockBootstrapLoginRunnerSuccessReturnsSessionPayload(t *testing.T) {
	t.Parallel()

	runner := NewMockBootstrapLoginRunner(&mockBootstrapResolver{}, nil, nil)
	result, err := runner.RunBootstrapLogin(context.Background(), testBootstrapLoginInput())
	if err != nil {
		t.Fatalf("RunBootstrapLogin() error = %v", err)
	}
	if result.Outcome != domain.BootstrapLoginOutcomeSuccess {
		t.Fatalf("expected outcome %s, got %s", domain.BootstrapLoginOutcomeSuccess, result.Outcome)
	}
	if len(result.SessionPayload) == 0 {
		t.Fatal("expected non-empty session payload on bootstrap success")
	}
}

func TestPlaywrightBootstrapLoginRunnerMapsAdapterRuntimeError(t *testing.T) {
	t.Parallel()

	runner := NewPlaywrightBootstrapLoginRunner(
		&mockBootstrapResolver{},
		nil,
		&mockPlaywrightBootstrapAdapter{
			outcome: domain.BootstrapLoginOutcomeAuthRuntimeError,
			payload: nil,
			err:     errors.New("playwright timeout"),
		},
	)

	result, err := runner.RunBootstrapLogin(context.Background(), testBootstrapLoginInput())
	if err != nil {
		t.Fatalf("RunBootstrapLogin() error = %v", err)
	}
	if result.Outcome != domain.BootstrapLoginOutcomeAuthRuntimeError {
		t.Fatalf("expected outcome %s, got %s", domain.BootstrapLoginOutcomeAuthRuntimeError, result.Outcome)
	}
}

func TestPlaywrightBootstrapLoginRunnerPropagatesResolverError(t *testing.T) {
	t.Parallel()

	expectedErr := domain.NewDomainError(
		domain.ErrorCodeAuthChallengeBlocked,
		"manual credential source requires operator interaction",
	)
	runner := NewPlaywrightBootstrapLoginRunner(
		&mockBootstrapResolver{
			resolveFn: func(
				ctx context.Context,
				source domain.CredentialSource,
				reference string,
			) (domain.AccountCredentials, error) {
				return domain.AccountCredentials{}, expectedErr
			},
		},
		nil,
		&mockPlaywrightBootstrapAdapter{
			outcome: domain.BootstrapLoginOutcomeSuccess,
			payload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		},
	)

	_, err := runner.RunBootstrapLogin(context.Background(), testBootstrapLoginInput())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected resolver error, got %v", err)
	}
}

func testBootstrapLoginInput() domain.BootstrapLoginInput {
	return domain.BootstrapLoginInput{
		TaskID:             uuid.New(),
		AccountID:          uuid.New(),
		Attempt:            1,
		ExecutionContextID: "worker-bootstrap-test",
		CredentialSource:   domain.CredentialSourceEnv,
		CredentialRef:      "env://FOLLOWER_LOGIN_USER,FOLLOWER_LOGIN_PASSWORD",
	}
}
