package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/observability"
	"follower/internal/repository"

	"github.com/google/uuid"
)

type sessionPayloadStore interface {
	Save(ctx context.Context, accountID uuid.UUID, revision int64, payload []byte) (string, error)
	Load(ctx context.Context, accountID uuid.UUID, objectKey string) ([]byte, error)
	Delete(ctx context.Context, objectKey string) error
}

type SessionRestorer struct {
	repository repository.SessionRepository
	store      sessionPayloadStore
	logger     *slog.Logger
}

func NewSessionRestorer(
	repository repository.SessionRepository,
	store sessionPayloadStore,
	logger *slog.Logger,
) *SessionRestorer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &SessionRestorer{
		repository: repository,
		store:      store,
		logger:     logger,
	}
}

func (r *SessionRestorer) Restore(
	ctx context.Context,
	accountID uuid.UUID,
) (domain.SessionMetadata, []byte, error) {
	startedAt := time.Now()
	restoreContext := observability.RestoreLifecycleContextFrom(ctx)
	r.logger.Info(
		observability.EventSessionRestoreStarted,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "browser.session_restorer",
				TaskID:     restoreContext.TaskID,
				AccountID:  accountID.String(),
				Attempt:    restoreContext.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: 0,
			},
		)...,
	)

	metadata, err := r.repository.GetByAccountID(ctx, accountID)
	if err != nil {
		return domain.SessionMetadata{}, nil, err
	}

	payload, err := r.store.Load(ctx, accountID, metadata.ObjectKey)
	if err != nil {
		status := statusForRestoreError(err)
		errorCode := resolveErrorCode(err)

		auditCtx := audit.WithActor(ctx, audit.Actor{
			Type: audit.ActorTypeInternalProcess,
			ID:   "browser.session_restorer",
		})
		if _, statusErr := r.repository.UpdateStatus(auditCtx, accountID, status, errorCode); statusErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to update session status: %w", statusErr))
		}

		r.logger.Warn(
			observability.EventSessionRestoreFailed,
			observability.ErrorLifecycleAttrs(
				observability.LifecycleContext{
					Component:  "browser.session_restorer",
					TaskID:     restoreContext.TaskID,
					AccountID:  accountID.String(),
					Attempt:    restoreContext.Attempt,
					ErrorCode:  string(errorCode),
					DurationMS: time.Since(startedAt).Milliseconds(),
				},
				"session restore failed",
				"session_revision", metadata.Revision,
				"object_key", metadata.ObjectKey,
			)...,
		)

		return metadata, nil, err
	}

	metadata, err = r.repository.MarkRestored(
		audit.WithActor(ctx, audit.Actor{
			Type: audit.ActorTypeInternalProcess,
			ID:   "browser.session_restorer",
		}),
		accountID,
	)
	if err != nil {
		return domain.SessionMetadata{}, nil, err
	}

	r.logger.Info(
		observability.EventSessionRestored,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "browser.session_restorer",
				TaskID:     restoreContext.TaskID,
				AccountID:  accountID.String(),
				Attempt:    restoreContext.Attempt,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"status", metadata.Status,
			"session_revision", metadata.Revision,
			"object_key", metadata.ObjectKey,
		)...,
	)

	return metadata, payload, nil
}

func (r *SessionRestorer) Save(
	ctx context.Context,
	accountID uuid.UUID,
	payload []byte,
) (domain.SessionMetadata, error) {
	startedAt := time.Now()

	nextRevision := int64(1)
	currentMetadata, err := r.repository.GetByAccountID(ctx, accountID)
	if err == nil {
		nextRevision = currentMetadata.Revision + 1
	} else if !domain.IsDomainErrorCode(err, domain.ErrorCodeSessionMetadataNotFound) {
		return domain.SessionMetadata{}, err
	}

	objectKey, err := r.store.Save(ctx, accountID, nextRevision, payload)
	if err != nil {
		return domain.SessionMetadata{}, err
	}

	metadata := domain.SessionMetadata{
		AccountID: accountID,
		Revision:  nextRevision,
		Status:    domain.SessionStatusValid,
		ObjectKey: objectKey,
	}

	savedMetadata, err := r.repository.Upsert(
		audit.WithActor(ctx, audit.Actor{
			Type: audit.ActorTypeInternalProcess,
			ID:   "browser.session_restorer",
		}),
		metadata,
	)
	if err != nil {
		if cleanupErr := r.store.Delete(ctx, objectKey); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to cleanup session object: %w", cleanupErr))
		}
		return domain.SessionMetadata{}, err
	}

	r.logger.Info(
		observability.EventSessionSaved,
		observability.LifecycleAttrs(
			observability.LifecycleContext{
				Component:  "browser.session_restorer",
				TaskID:     "n/a",
				AccountID:  accountID.String(),
				Attempt:    0,
				ErrorCode:  string(domain.ErrorCodeEligible),
				DurationMS: time.Since(startedAt).Milliseconds(),
			},
			"session_revision", savedMetadata.Revision,
			"object_key", savedMetadata.ObjectKey,
		)...,
	)

	return savedMetadata, nil
}

func statusForRestoreError(err error) domain.SessionStatus {
	switch {
	case domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing):
		return domain.SessionStatusUnavailable
	case domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadCorrupted):
		return domain.SessionStatusInvalid
	case domain.IsDomainErrorCode(err, domain.ErrorCodeSessionOwnershipMismatch):
		return domain.SessionStatusInvalid
	default:
		return domain.SessionStatusInvalid
	}
}

func resolveErrorCode(err error) domain.ErrorCode {
	var domainErr *domain.DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return domain.ErrorCodeInternal
}

func BootstrapReasonForRestoreError(err error) (domain.ErrorCode, bool) {
	switch {
	case domain.IsDomainErrorCode(err, domain.ErrorCodeSessionMetadataNotFound):
		return domain.ErrorCodeAuthBootstrapRequired, true
	case domain.IsDomainErrorCode(err, domain.ErrorCodeSessionPayloadMissing):
		return domain.ErrorCodeAuthBootstrapRequired, true
	default:
		return "", false
	}
}
