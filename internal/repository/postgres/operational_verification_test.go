package postgres_test

import (
	"context"
	"testing"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/observability"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
)

func TestOperationalVerificationCorrelatesReadinessWithAuditTrail(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)

	auditRepository := postgresrepo.NewAuditRepository(pool)
	auditLog := audit.NewLog(auditRepository, nil)
	accountRepository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails(), auditLog)

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9055,
		IsActive: true,
	}
	if err := accountRepository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	accountID := uuid.New()
	if err := accountRepository.CreateAccount(context.Background(), domain.Account{
		ID:               accountID,
		Username:         "audit-op-verification-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	ctx := audit.WithActor(context.Background(), audit.Actor{
		Type: audit.ActorTypeInternalProcess,
		ID:   "quota-service",
	})
	if err := accountRepository.UpdateAccountState(
		ctx,
		accountID,
		domain.AccountStateQuarantined,
		false,
		true,
		false,
		true,
	); err != nil {
		t.Fatalf("UpdateAccountState() error = %v", err)
	}

	healthService := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		time.Second,
		"0.1.0",
		map[string]string{
			"audit_trail_source": "postgres.audit_logs",
		},
	)
	snapshot := healthService.Snapshot(context.Background())
	if snapshot.Status != observability.StatusReady {
		t.Fatalf("expected readiness status %q, got %q", observability.StatusReady, snapshot.Status)
	}

	records, err := auditRepository.ListRecent(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected audit trail records after account state change")
	}
	if records[0].Action != "account.state_changed" {
		t.Fatalf("expected action %q, got %q", "account.state_changed", records[0].Action)
	}
}
