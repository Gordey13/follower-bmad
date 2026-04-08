package audit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	Actor            Actor
	Action           string
	TargetType       string
	TargetID         string
	ChangeSummary    string
	DiagnosticFields map[string]string
}

type Record struct {
	ID               uuid.UUID
	ActorType        ActorType
	ActorID          string
	Action           string
	TargetType       string
	TargetID         string
	ChangeSummary    string
	DiagnosticFields map[string]string
	CreatedAt        time.Time
}

type Store interface {
	Append(ctx context.Context, record Record) (Record, error)
	ListRecent(ctx context.Context, limit int) ([]Record, error)
}

type Log struct {
	store  Store
	logger *slog.Logger
}

func NewLog(store Store, logger *slog.Logger) *Log {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Log{
		store:  store,
		logger: logger,
	}
}

func (l *Log) Record(ctx context.Context, event Event) (Record, error) {
	if l.store == nil {
		return Record{}, fmt.Errorf("audit store is not configured")
	}

	if err := validateEvent(event); err != nil {
		return Record{}, err
	}

	actor := normalizeActor(event.Actor)
	if event.Actor.Type == "" && event.Actor.ID == "" {
		actor = ActorFromContext(ctx)
	}

	record := Record{
		ID:               uuid.New(),
		ActorType:        actor.Type,
		ActorID:          actor.ID,
		Action:           strings.TrimSpace(event.Action),
		TargetType:       strings.TrimSpace(event.TargetType),
		TargetID:         strings.TrimSpace(event.TargetID),
		ChangeSummary:    strings.TrimSpace(event.ChangeSummary),
		DiagnosticFields: SanitizeDiagnosticFields(event.DiagnosticFields),
		CreatedAt:        time.Now().UTC(),
	}

	stored, err := l.store.Append(ctx, record)
	if err != nil {
		l.logger.Error("audit.record_failed",
			"action", record.Action,
			"target_type", record.TargetType,
			"target_id", record.TargetID,
		)
		return Record{}, err
	}

	return stored, nil
}

func validateEvent(event Event) error {
	if strings.TrimSpace(event.Action) == "" {
		return fmt.Errorf("audit action is required")
	}
	if strings.TrimSpace(event.TargetType) == "" {
		return fmt.Errorf("audit target_type is required")
	}
	if strings.TrimSpace(event.TargetID) == "" {
		return fmt.Errorf("audit target_id is required")
	}
	if strings.TrimSpace(event.ChangeSummary) == "" {
		return fmt.Errorf("audit change_summary is required")
	}

	return nil
}

func SanitizeDiagnosticFields(fields map[string]string) map[string]string {
	if len(fields) == 0 {
		return map[string]string{}
	}

	safe := make(map[string]string, len(fields))
	for key, value := range fields {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey == "" {
			continue
		}
		if containsSensitiveToken(normalizedKey) {
			continue
		}

		normalizedValue := strings.TrimSpace(value)
		if len(normalizedValue) > 256 {
			normalizedValue = normalizedValue[:256]
		}
		safe[normalizedKey] = normalizedValue
	}

	return safe
}

func containsSensitiveToken(value string) bool {
	sensitiveTokens := []string{
		"secret",
		"password",
		"token",
		"cookie",
		"session_payload",
		"raw_session",
		"access_key",
		"proxy_credential",
		"credentials",
	}

	return slices.ContainsFunc(sensitiveTokens, func(token string) bool {
		return strings.Contains(value, token)
	})
}
