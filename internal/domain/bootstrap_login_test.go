package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestCredentialSourceValidation(t *testing.T) {
	t.Parallel()

	if !CredentialSourceEnv.IsValid() {
		t.Fatal("expected env credential source to be valid")
	}
	if CredentialSource("secret-store").IsValid() {
		t.Fatal("expected unknown credential source to be invalid")
	}
	if got := NormalizeCredentialSource("unknown"); got != CredentialSourceManual {
		t.Fatalf("expected unknown source to normalize to manual, got %s", got)
	}
}

func TestAccountCredentialsValidate(t *testing.T) {
	t.Parallel()

	if err := (AccountCredentials{
		Username: "user@example.com",
		Password: "secret",
	}).Validate(); err != nil {
		t.Fatalf("expected credentials to be valid, got %v", err)
	}

	if err := (AccountCredentials{
		Username: "",
		Password: "secret",
	}).Validate(); !IsDomainErrorCode(err, ErrorCodeAuthInvalidCredentials) {
		t.Fatalf("expected %s, got %v", ErrorCodeAuthInvalidCredentials, err)
	}
}

func TestBootstrapLoginInputValidate(t *testing.T) {
	t.Parallel()

	input := BootstrapLoginInput{
		TaskID:             uuid.New(),
		AccountID:          uuid.New(),
		Attempt:            1,
		ExecutionContextID: "worker-bootstrap-test",
		CredentialSource:   CredentialSourceEnv,
		CredentialRef:      "env://FOLLOWER_LOGIN_USER,FOLLOWER_LOGIN_PASSWORD",
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("expected valid bootstrap input, got %v", err)
	}

	invalid := input
	invalid.CredentialRef = ""
	if err := invalid.Validate(); !IsDomainErrorCode(err, ErrorCodeAuthBootstrapFailed) {
		t.Fatalf("expected %s, got %v", ErrorCodeAuthBootstrapFailed, err)
	}
}

func TestBootstrapLoginResultValidate(t *testing.T) {
	t.Parallel()

	success := BootstrapLoginResult{
		Outcome:        BootstrapLoginOutcomeSuccess,
		SessionPayload: []byte(`{"cookies":[{"name":"sid","value":"ok"}]}`),
		Diagnostics: BootstrapLoginDiagnostics{
			Engine:     "mock",
			DurationMS: 1,
		},
	}
	if err := success.Validate(); err != nil {
		t.Fatalf("expected valid success result, got %v", err)
	}

	invalid := success
	invalid.SessionPayload = []byte(`not-json`)
	if err := invalid.Validate(); !IsDomainErrorCode(err, ErrorCodeSessionPayloadInvalid) {
		t.Fatalf("expected %s, got %v", ErrorCodeSessionPayloadInvalid, err)
	}
}
