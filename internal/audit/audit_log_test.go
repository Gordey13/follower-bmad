package audit

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type mockStore struct {
	appendFn func(ctx context.Context, record Record) (Record, error)
}

func (m *mockStore) Append(ctx context.Context, record Record) (Record, error) {
	if m.appendFn != nil {
		return m.appendFn(ctx, record)
	}
	return record, nil
}

func (m *mockStore) ListRecent(ctx context.Context, limit int) ([]Record, error) {
	return nil, nil
}

func TestRecordSanitizesSensitiveDiagnosticFields(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		appendFn: func(ctx context.Context, record Record) (Record, error) {
			if record.ActorType != ActorTypeAdminOperator {
				t.Fatalf("expected actor type %q, got %q", ActorTypeAdminOperator, record.ActorType)
			}
			if record.ActorID != "admin-operator-01" {
				t.Fatalf("expected actor id %q, got %q", "admin-operator-01", record.ActorID)
			}
			if _, ok := record.DiagnosticFields["session_payload"]; ok {
				t.Fatal("session_payload must be removed by sanitization")
			}
			if _, ok := record.DiagnosticFields["minio_secret_key"]; ok {
				t.Fatal("minio_secret_key must be removed by sanitization")
			}
			if got := record.DiagnosticFields["status"]; got != "restricted" {
				t.Fatalf("expected status diagnostic field to be preserved, got %q", got)
			}
			if got := record.DiagnosticFields["error_code"]; got != "session_payload_missing" {
				t.Fatalf("expected error_code diagnostic field to be preserved, got %q", got)
			}
			return record, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	log := NewLog(store, logger)

	ctx := WithActor(context.Background(), Actor{
		Type: ActorTypeAdminOperator,
		ID:   "admin-operator-01",
	})

	_, err := log.Record(ctx, Event{
		Action:        "account.state_changed",
		TargetType:    "account",
		TargetID:      "acc-01",
		ChangeSummary: "account moved to restricted",
		DiagnosticFields: map[string]string{
			"status":           "restricted",
			"error_code":       "session_payload_missing",
			"session_payload":  "{\"cookies\":[]}",
			"minio_secret_key": "super-secret",
		},
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func TestRecordUsesSystemActorWhenNoActorInContext(t *testing.T) {
	t.Parallel()

	store := &mockStore{
		appendFn: func(ctx context.Context, record Record) (Record, error) {
			if record.ActorType != ActorTypeSystem {
				t.Fatalf("expected actor type %q, got %q", ActorTypeSystem, record.ActorType)
			}
			if record.ActorID != "system" {
				t.Fatalf("expected actor id %q, got %q", "system", record.ActorID)
			}
			return record, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	log := NewLog(store, logger)

	_, err := log.Record(context.Background(), Event{
		Action:        "readiness.baseline_verified",
		TargetType:    "service",
		TargetID:      "follower",
		ChangeSummary: "technical readiness baseline initialized",
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}
