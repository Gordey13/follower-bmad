package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"follower/internal/audit"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditPostgresRepository struct {
	pool *pgxpool.Pool
}

func NewAuditRepository(pool *pgxpool.Pool) *AuditPostgresRepository {
	return &AuditPostgresRepository{pool: pool}
}

func (r *AuditPostgresRepository) Append(ctx context.Context, record audit.Record) (audit.Record, error) {
	diagnosticFieldsJSON, err := json.Marshal(record.DiagnosticFields)
	if err != nil {
		return audit.Record{}, fmt.Errorf("marshal diagnostic fields: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO audit_logs (
			id,
			actor_type,
			actor_id,
			action,
			target_type,
			target_id,
			change_summary,
			diagnostic_fields,
			created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING
			id,
			actor_type,
			actor_id,
			action,
			target_type,
			target_id,
			change_summary,
			diagnostic_fields,
			created_at
	`,
		record.ID,
		string(record.ActorType),
		record.ActorID,
		record.Action,
		record.TargetType,
		record.TargetID,
		record.ChangeSummary,
		diagnosticFieldsJSON,
		record.CreatedAt,
	)

	return scanAuditRecord(row)
}

func (r *AuditPostgresRepository) ListRecent(ctx context.Context, limit int) ([]audit.Record, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			actor_type,
			actor_id,
			action,
			target_type,
			target_id,
			change_summary,
			diagnostic_fields,
			created_at
		FROM audit_logs
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]audit.Record, 0, limit)
	for rows.Next() {
		record, scanErr := scanAuditRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return records, nil
}

func scanAuditRecord(row pgx.Row) (audit.Record, error) {
	var record audit.Record
	var actorType string
	var diagnosticFieldsJSON []byte
	err := row.Scan(
		&record.ID,
		&actorType,
		&record.ActorID,
		&record.Action,
		&record.TargetType,
		&record.TargetID,
		&record.ChangeSummary,
		&diagnosticFieldsJSON,
		&record.CreatedAt,
	)
	if err != nil {
		return audit.Record{}, err
	}

	record.ActorType = audit.ActorType(actorType)
	if len(diagnosticFieldsJSON) > 0 {
		if err := json.Unmarshal(diagnosticFieldsJSON, &record.DiagnosticFields); err != nil {
			return audit.Record{}, fmt.Errorf("unmarshal diagnostic fields: %w", err)
		}
	} else {
		record.DiagnosticFields = map[string]string{}
	}

	return record, nil
}
