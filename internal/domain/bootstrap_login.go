package domain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type CredentialSource string

const (
	CredentialSourceEnv    CredentialSource = "env"
	CredentialSourceVault  CredentialSource = "vault"
	CredentialSourceFile   CredentialSource = "file"
	CredentialSourceManual CredentialSource = "manual"
)

func (source CredentialSource) IsValid() bool {
	switch source {
	case CredentialSourceEnv, CredentialSourceVault, CredentialSourceFile, CredentialSourceManual:
		return true
	default:
		return false
	}
}

func NormalizeCredentialSource(source CredentialSource) CredentialSource {
	if source.IsValid() {
		return source
	}
	return CredentialSourceManual
}

type AccountCredentials struct {
	Username string
	Password string
}

func (credentials AccountCredentials) Validate() error {
	if strings.TrimSpace(credentials.Username) == "" {
		return NewDomainError(
			ErrorCodeAuthInvalidCredentials,
			"credential username must not be empty",
		)
	}
	if strings.TrimSpace(credentials.Password) == "" {
		return NewDomainError(
			ErrorCodeAuthInvalidCredentials,
			"credential password must not be empty",
		)
	}
	return nil
}

type BootstrapLoginOutcome string

const (
	BootstrapLoginOutcomeSuccess                BootstrapLoginOutcome = "success"
	BootstrapLoginOutcomeAuthChallenge          BootstrapLoginOutcome = "auth_challenge"
	BootstrapLoginOutcomeAuthInvalidCredentials BootstrapLoginOutcome = "auth_invalid_credentials"
	BootstrapLoginOutcomeAuthRuntimeError       BootstrapLoginOutcome = "auth_runtime_error"
)

func (outcome BootstrapLoginOutcome) IsValid() bool {
	switch outcome {
	case BootstrapLoginOutcomeSuccess,
		BootstrapLoginOutcomeAuthChallenge,
		BootstrapLoginOutcomeAuthInvalidCredentials,
		BootstrapLoginOutcomeAuthRuntimeError:
		return true
	default:
		return false
	}
}

type BootstrapLoginInput struct {
	TaskID             uuid.UUID
	AccountID          uuid.UUID
	Attempt            int
	ExecutionContextID string
	CredentialSource   CredentialSource
	CredentialRef      string
}

func (input BootstrapLoginInput) Validate() error {
	if input.TaskID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidTaskIdentifier,
			"bootstrap login input task id must not be empty",
		)
	}
	if input.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"bootstrap login input account id must not be empty",
		)
	}
	if strings.TrimSpace(input.ExecutionContextID) == "" {
		return NewDomainError(
			ErrorCodeInvalidExecutionContext,
			"bootstrap login input execution context id must not be empty",
		)
	}
	if input.Attempt <= 0 {
		return NewDomainError(
			ErrorCodeInvalidTaskTransition,
			fmt.Sprintf("bootstrap login input attempt must be > 0, got %d", input.Attempt),
		)
	}
	if !NormalizeCredentialSource(input.CredentialSource).IsValid() {
		return NewDomainError(
			ErrorCodeAuthBootstrapFailed,
			"bootstrap login credential source is invalid",
		)
	}
	if strings.TrimSpace(input.CredentialRef) == "" {
		return NewDomainError(
			ErrorCodeAuthBootstrapFailed,
			"bootstrap login credential ref must not be empty",
		)
	}

	return nil
}

type BootstrapLoginDiagnostics struct {
	Engine     string
	DurationMS int64
}

type BootstrapLoginResult struct {
	Outcome        BootstrapLoginOutcome
	SessionPayload []byte
	Diagnostics    BootstrapLoginDiagnostics
}

func (result BootstrapLoginResult) Validate() error {
	if !result.Outcome.IsValid() {
		return NewDomainError(
			ErrorCodeAuthBootstrapFailed,
			fmt.Sprintf("invalid bootstrap login outcome: %s", result.Outcome),
		)
	}
	if result.Outcome == BootstrapLoginOutcomeSuccess {
		if len(result.SessionPayload) == 0 {
			return NewDomainError(
				ErrorCodeSessionPayloadMissing,
				"bootstrap login success result requires non-empty session payload",
			)
		}
		if !json.Valid(result.SessionPayload) {
			return NewDomainError(
				ErrorCodeSessionPayloadInvalid,
				"bootstrap login success session payload must be valid JSON",
			)
		}
	}
	if result.Diagnostics.DurationMS < 0 {
		return NewDomainError(
			ErrorCodeInternal,
			"bootstrap login diagnostics duration must be >= 0",
		)
	}

	return nil
}
