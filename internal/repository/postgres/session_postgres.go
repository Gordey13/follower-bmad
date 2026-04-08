package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionPostgresRepository struct {
	pool     *pgxpool.Pool
	auditLog *audit.Log
}

func NewSessionRepository(pool *pgxpool.Pool, auditLog ...*audit.Log) *SessionPostgresRepository {
	var logger *audit.Log
	if len(auditLog) > 0 {
		logger = auditLog[0]
	}

	return &SessionPostgresRepository{
		pool:     pool,
		auditLog: logger,
	}
}

func (r *SessionPostgresRepository) GetByAccountID(ctx context.Context, accountID uuid.UUID) (domain.SessionMetadata, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			account_id,
			revision,
			status,
			object_key,
			COALESCE(error_code, ''),
			created_at,
			updated_at,
			last_restored_at
		FROM account_sessions
		WHERE account_id = $1
	`, accountID)

	metadata, err := scanSessionMetadata(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SessionMetadata{}, domain.NewDomainError(
			domain.ErrorCodeSessionMetadataNotFound,
			fmt.Sprintf("session metadata for account %s not found", accountID.String()),
		)
	}

	return metadata, err
}

func (r *SessionPostgresRepository) StatusSnapshot(ctx context.Context) (map[domain.SessionStatus]int64, error) {
	snapshot := map[domain.SessionStatus]int64{
		domain.SessionStatusValid:       0,
		domain.SessionStatusInvalid:     0,
		domain.SessionStatusUnavailable: 0,
	}

	rows, err := r.pool.Query(ctx, `
		SELECT status, COUNT(*)::BIGINT
		FROM account_sessions
		WHERE status IN ('valid', 'invalid', 'unavailable')
		GROUP BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}

		normalizedStatus := domain.SessionStatus(status)
		if !normalizedStatus.IsValid() {
			continue
		}
		snapshot[normalizedStatus] = count
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func (r *SessionPostgresRepository) Upsert(
	ctx context.Context,
	metadata domain.SessionMetadata,
) (domain.SessionMetadata, error) {
	if err := metadata.Validate(); err != nil {
		return domain.SessionMetadata{}, err
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO account_sessions (
			account_id,
			revision,
			status,
			object_key,
			error_code,
			last_restored_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (account_id) DO UPDATE
		SET revision = EXCLUDED.revision,
			status = EXCLUDED.status,
			object_key = EXCLUDED.object_key,
			error_code = EXCLUDED.error_code,
			last_restored_at = COALESCE(EXCLUDED.last_restored_at, account_sessions.last_restored_at),
			updated_at = NOW()
		RETURNING
			account_id,
			revision,
			status,
			object_key,
			COALESCE(error_code, ''),
			created_at,
			updated_at,
			last_restored_at
	`,
		metadata.AccountID,
		metadata.Revision,
		metadata.Status,
		metadata.ObjectKey,
		nullableErrorCode(metadata.ErrorCode),
		metadata.LastRestoredAt,
	)

	updatedMetadata, err := scanSessionMetadata(row)
	if err != nil {
		return domain.SessionMetadata{}, err
	}

	if r.auditLog != nil {
		_, auditErr := r.auditLog.Record(ctx, audit.Event{
			Action:        "session.metadata_upserted",
			TargetType:    "session",
			TargetID:      metadata.AccountID.String(),
			ChangeSummary: "session metadata upserted",
			DiagnosticFields: map[string]string{
				"revision":   strconv.FormatInt(updatedMetadata.Revision, 10),
				"status":     string(updatedMetadata.Status),
				"error_code": string(updatedMetadata.ErrorCode),
			},
		})
		if auditErr != nil {
			// Domain write is already committed; audit is best-effort and must not break session persistence.
		}
	}

	return updatedMetadata, nil
}

func (r *SessionPostgresRepository) UpdateStatus(
	ctx context.Context,
	accountID uuid.UUID,
	status domain.SessionStatus,
	errorCode domain.ErrorCode,
) (domain.SessionMetadata, error) {
	if !status.IsValid() {
		return domain.SessionMetadata{}, domain.NewDomainError(
			domain.ErrorCodeInvalidSessionStatus,
			fmt.Sprintf("invalid session status: %s", status),
		)
	}
	if status == domain.SessionStatusValid && errorCode != "" {
		return domain.SessionMetadata{}, domain.NewDomainError(
			domain.ErrorCodeInvalidSessionStatus,
			"valid session status must not have error code",
		)
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE account_sessions
		SET status = $2,
			error_code = $3,
			updated_at = NOW()
		WHERE account_id = $1
		RETURNING
			account_id,
			revision,
			status,
			object_key,
			COALESCE(error_code, ''),
			created_at,
			updated_at,
			last_restored_at
	`, accountID, status, nullableErrorCode(errorCode))

	metadata, err := scanSessionMetadata(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SessionMetadata{}, domain.NewDomainError(
			domain.ErrorCodeSessionMetadataNotFound,
			fmt.Sprintf("session metadata for account %s not found", accountID.String()),
		)
	}

	if err != nil {
		return domain.SessionMetadata{}, err
	}

	if r.auditLog != nil {
		_, auditErr := r.auditLog.Record(ctx, audit.Event{
			Action:        "session.status_changed",
			TargetType:    "session",
			TargetID:      accountID.String(),
			ChangeSummary: fmt.Sprintf("session status changed to %s", status),
			DiagnosticFields: map[string]string{
				"status":     string(metadata.Status),
				"error_code": string(metadata.ErrorCode),
			},
		})
		if auditErr != nil {
			// Domain write is already committed; audit is best-effort and must not break status updates.
		}
	}

	return metadata, nil
}

func (r *SessionPostgresRepository) MarkRestored(
	ctx context.Context,
	accountID uuid.UUID,
) (domain.SessionMetadata, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE account_sessions
		SET status = $2,
			error_code = NULL,
			last_restored_at = NOW(),
			updated_at = NOW()
		WHERE account_id = $1
		RETURNING
			account_id,
			revision,
			status,
			object_key,
			COALESCE(error_code, ''),
			created_at,
			updated_at,
			last_restored_at
	`, accountID, domain.SessionStatusValid)

	metadata, err := scanSessionMetadata(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SessionMetadata{}, domain.NewDomainError(
			domain.ErrorCodeSessionMetadataNotFound,
			fmt.Sprintf("session metadata for account %s not found", accountID.String()),
		)
	}

	if err != nil {
		return domain.SessionMetadata{}, err
	}

	if r.auditLog != nil {
		_, auditErr := r.auditLog.Record(ctx, audit.Event{
			Action:        "session.restored",
			TargetType:    "session",
			TargetID:      accountID.String(),
			ChangeSummary: "session restored and marked valid",
			DiagnosticFields: map[string]string{
				"revision": strconv.FormatInt(metadata.Revision, 10),
				"status":   string(metadata.Status),
			},
		})
		if auditErr != nil {
			// Domain write is already committed; audit is best-effort and must not break restore transitions.
		}
	}

	return metadata, nil
}

func scanSessionMetadata(row pgx.Row) (domain.SessionMetadata, error) {
	var metadata domain.SessionMetadata
	var status string
	var errorCode string
	var lastRestoredAt *time.Time

	err := row.Scan(
		&metadata.AccountID,
		&metadata.Revision,
		&status,
		&metadata.ObjectKey,
		&errorCode,
		&metadata.CreatedAt,
		&metadata.UpdatedAt,
		&lastRestoredAt,
	)
	if err != nil {
		return domain.SessionMetadata{}, err
	}

	metadata.Status = domain.SessionStatus(status)
	metadata.ErrorCode = domain.ErrorCode(errorCode)
	metadata.LastRestoredAt = lastRestoredAt

	return metadata, nil
}

func nullableErrorCode(code domain.ErrorCode) interface{} {
	if code == "" {
		return nil
	}
	return string(code)
}
