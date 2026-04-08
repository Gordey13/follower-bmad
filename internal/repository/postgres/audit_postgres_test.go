package postgres_test

import (
	"context"
	"testing"

	"follower/internal/audit"
	postgresrepo "follower/internal/repository/postgres"
)

func TestAuditRepositoryAppendAndListRecent(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)

	repository := postgresrepo.NewAuditRepository(pool)
	logger := audit.NewLog(repository, nil)

	_, err := logger.Record(context.Background(), audit.Event{
		Action:        "account.state_changed",
		TargetType:    "account",
		TargetID:      "acc-01",
		ChangeSummary: "account moved to quarantined",
		DiagnosticFields: map[string]string{
			"state": "quarantined",
		},
	})
	if err != nil {
		t.Fatalf("Record(account.state_changed) error = %v", err)
	}

	_, err = logger.Record(context.Background(), audit.Event{
		Action:        "session.status_changed",
		TargetType:    "session",
		TargetID:      "acc-01",
		ChangeSummary: "session status changed to invalid",
		DiagnosticFields: map[string]string{
			"status":     "invalid",
			"error_code": "session_payload_missing",
		},
	})
	if err != nil {
		t.Fatalf("Record(session.status_changed) error = %v", err)
	}

	records, err := repository.ListRecent(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected at least 2 records, got %d", len(records))
	}
	if records[0].Action != "session.status_changed" {
		t.Fatalf("expected latest action %q, got %q", "session.status_changed", records[0].Action)
	}
	if records[1].Action != "account.state_changed" {
		t.Fatalf("expected previous action %q, got %q", "account.state_changed", records[1].Action)
	}
}
