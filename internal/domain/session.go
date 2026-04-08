package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type SessionStatus string

const (
	SessionStatusValid       SessionStatus = "valid"
	SessionStatusInvalid     SessionStatus = "invalid"
	SessionStatusUnavailable SessionStatus = "unavailable"
)

func (status SessionStatus) IsValid() bool {
	switch status {
	case SessionStatusValid, SessionStatusInvalid, SessionStatusUnavailable:
		return true
	default:
		return false
	}
}

type SessionMetadata struct {
	AccountID      uuid.UUID
	Revision       int64
	Status         SessionStatus
	ObjectKey      string
	ErrorCode      ErrorCode
	LastRestoredAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (metadata SessionMetadata) Validate() error {
	if metadata.AccountID == uuid.Nil {
		return NewDomainError(
			ErrorCodeInvalidAccountIdentifier,
			"session metadata requires non-empty account id",
		)
	}
	if metadata.Revision <= 0 {
		return NewDomainError(
			ErrorCodeInvalidSessionRevision,
			fmt.Sprintf("session revision must be positive, got %d", metadata.Revision),
		)
	}
	if !metadata.Status.IsValid() {
		return NewDomainError(
			ErrorCodeInvalidSessionStatus,
			fmt.Sprintf("invalid session status: %s", metadata.Status),
		)
	}
	if metadata.ObjectKey == "" {
		return NewDomainError(
			ErrorCodeInvalidSessionObjectKey,
			"session object key must not be empty",
		)
	}
	if metadata.Status == SessionStatusValid && metadata.ErrorCode != "" {
		return NewDomainError(
			ErrorCodeInvalidSessionStatus,
			"valid session metadata must not contain error code",
		)
	}

	return nil
}
