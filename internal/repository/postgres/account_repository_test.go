package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	postgresrepo "follower/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateAndReadAccountWithProxy(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9050,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-read-01",
		ProxyID:          proxy.ID,
		CredentialSource: domain.CredentialSourceEnv,
		CredentialRef:    "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	got, err := repository.GetAccountWithProxy(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("GetAccountWithProxy() error = %v", err)
	}

	if got.Account.ID != account.ID {
		t.Fatalf("expected account id %s, got %s", account.ID.String(), got.Account.ID.String())
	}
	if got.Proxy.ID != proxy.ID {
		t.Fatalf("expected proxy id %s, got %s", proxy.ID.String(), got.Proxy.ID.String())
	}
	if got.Account.CredentialSource != domain.CredentialSourceEnv {
		t.Fatalf("expected credential source %s, got %s", domain.CredentialSourceEnv, got.Account.CredentialSource)
	}
	if got.Account.CredentialRef != "env://FOLLOWER_BOOTSTRAP_USER,FOLLOWER_BOOTSTRAP_PASSWORD" {
		t.Fatalf("unexpected credential ref %s", got.Account.CredentialRef)
	}
}

func TestCreateAccountFailsWithoutProxyBinding(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-missing-proxy",
		ProxyID:          uuid.New(),
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}

	err := repository.CreateAccount(context.Background(), account)
	if err == nil {
		t.Fatal("expected CreateAccount() to fail when proxy binding is missing")
	}
}

func TestCreateAndReadAccountWithoutProxyBinding(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-without-proxy-01",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}

	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	got, err := repository.GetAccountWithProxy(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("GetAccountWithProxy() error = %v", err)
	}

	if got.Account.ID != account.ID {
		t.Fatalf("expected account id %s, got %s", account.ID.String(), got.Account.ID.String())
	}
	if got.Account.ProxyID != uuid.Nil {
		t.Fatalf("expected account proxy id to be empty, got %s", got.Account.ProxyID.String())
	}
	if got.Proxy.ID != uuid.Nil {
		t.Fatalf("expected empty proxy in optional path, got %s", got.Proxy.ID.String())
	}
}

func TestCheckEligibilityAllowsProxylessAccountWhenBindingDisabled(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, proxyBindingDisabledGuardrails())

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-proxyless-eligible-01",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	decision, err := repository.CheckEligibility(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("CheckEligibility() error = %v", err)
	}
	if !decision.Eligible {
		t.Fatalf("expected account to be eligible in proxy-off mode, got %+v", decision)
	}
	if decision.ReasonCode != domain.ErrorCodeEligible {
		t.Fatalf("expected reason %s, got %s", domain.ErrorCodeEligible, decision.ReasonCode)
	}
}

func TestClaimAccountRejectsProxylessAccountWhenBindingRequired(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-proxyless-rejected-01",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	_, err := repository.ClaimAccount(context.Background(), account.ID, "exec-proxy-required")
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeAccountMissingProxy) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeAccountMissingProxy, err)
	}
}

func TestClaimAccountAllowsProxylessAccountWhenBindingDisabled(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, proxyBindingDisabledGuardrails())

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-proxyless-claim-01",
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	claimed, err := repository.ClaimAccount(context.Background(), account.ID, "exec-proxy-disabled")
	if err != nil {
		t.Fatalf("ClaimAccount() error = %v", err)
	}
	if claimed.ActiveExecutionContextID != "exec-proxy-disabled" {
		t.Fatalf("expected active execution context exec-proxy-disabled, got %s", claimed.ActiveExecutionContextID)
	}
}

func TestCheckEligibilityReturnsRestrictiveReason(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9051,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-restricted-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateRestricted,
		IsActive:         true,
		IsReady:          true,
		IsRestricted:     true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	decision, err := repository.CheckEligibility(context.Background(), account.ID)
	if err != nil {
		t.Fatalf("CheckEligibility() error = %v", err)
	}
	if decision.Eligible {
		t.Fatalf("expected account to be ineligible, got %+v", decision)
	}
	if decision.ReasonCode != domain.ErrorCodeAccountRestricted {
		t.Fatalf("expected reason %s, got %s", domain.ErrorCodeAccountRestricted, decision.ReasonCode)
	}
}

func TestClaimAccountPreventsConcurrentUse(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9052,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-concurrency-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	type claimResult struct {
		account domain.Account
		err     error
	}

	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	results := make(chan claimResult, 2)
	workers := []string{"exec-1", "exec-2"}

	waitGroup.Add(len(workers))
	for _, workerID := range workers {
		workerID := workerID
		go func() {
			defer waitGroup.Done()
			<-start
			claimed, err := repository.ClaimAccount(context.Background(), account.ID, workerID)
			results <- claimResult{account: claimed, err: err}
		}()
	}

	close(start)
	waitGroup.Wait()
	close(results)

	successCount := 0
	busyCount := 0
	for result := range results {
		if result.err == nil {
			successCount++
			continue
		}
		if domain.IsDomainErrorCode(result.err, domain.ErrorCodeAccountBusy) {
			busyCount++
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successCount)
	}
	if busyCount != 1 {
		t.Fatalf("expected exactly one busy rejection, got %d", busyCount)
	}
}

func TestClaimAccountIsIdempotentForSameExecutionContext(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9053,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-idempotent-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	firstClaim, err := repository.ClaimAccount(context.Background(), account.ID, "exec-1")
	if err != nil {
		t.Fatalf("first ClaimAccount() error = %v", err)
	}

	secondClaim, err := repository.ClaimAccount(context.Background(), account.ID, "exec-1")
	if err != nil {
		t.Fatalf("second ClaimAccount() should be idempotent, got error = %v", err)
	}

	if firstClaim.ID != secondClaim.ID {
		t.Fatalf("expected same account id on idempotent claim, got %s and %s", firstClaim.ID, secondClaim.ID)
	}
	if secondClaim.ActiveExecutionContextID != "exec-1" {
		t.Fatalf("expected active execution context exec-1, got %s", secondClaim.ActiveExecutionContextID)
	}
}

func TestClaimAccountRejectsEmptyExecutionContext(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9054,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	account := domain.Account{
		ID:               uuid.New(),
		Username:         "account-empty-exec-ctx-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}
	if err := repository.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	_, err := repository.ClaimAccount(context.Background(), account.ID, "")
	if err == nil {
		t.Fatal("expected error for empty execution context")
	}
	if !domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidExecutionContext) {
		t.Fatalf("expected error code %s, got %v", domain.ErrorCodeInvalidExecutionContext, err)
	}
}

func TestClaimAndReleaseWriteLifecycleAuditEvents(t *testing.T) {
	pool := mustOpenTestPool(t)
	prepareAuditLogsSchema(t, pool)

	auditRepository := postgresrepo.NewAuditRepository(pool)
	auditLog := audit.NewLog(auditRepository, nil)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails(), auditLog)

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9055,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	accountID := uuid.New()
	if err := repository.CreateAccount(context.Background(), domain.Account{
		ID:               accountID,
		Username:         "account-lifecycle-audit-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	claimed, err := repository.ClaimAccount(context.Background(), accountID, "exec-audit-01")
	if err != nil {
		t.Fatalf("ClaimAccount() error = %v", err)
	}
	if claimed.OperationalState != domain.AccountStateBusy {
		t.Fatalf("expected busy state after claim, got %s", claimed.OperationalState)
	}

	if err := repository.ReleaseAccount(context.Background(), accountID, "exec-audit-01"); err != nil {
		t.Fatalf("ReleaseAccount() error = %v", err)
	}

	records, err := auditRepository.ListRecent(context.Background(), 20)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	foundClaim := false
	foundRelease := false
	for _, record := range records {
		if record.TargetID != accountID.String() {
			continue
		}

		switch record.Action {
		case "account.claimed":
			foundClaim = true
			if record.DiagnosticFields["execution_context_id"] != "exec-audit-01" {
				t.Fatalf("expected claim execution_context_id to be set, got %+v", record.DiagnosticFields)
			}
		case "account.released":
			foundRelease = true
			if record.DiagnosticFields["execution_context_id"] != "exec-audit-01" {
				t.Fatalf("expected release execution_context_id to be set, got %+v", record.DiagnosticFields)
			}
		}
	}

	if !foundClaim {
		t.Fatal("expected account.claimed audit event")
	}
	if !foundRelease {
		t.Fatal("expected account.released audit event")
	}
}

func TestUpdateAccountStateSucceedsWhenAuditFails(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(
		pool,
		domain.DefaultRuntimeGuardrails(),
		newFailingAuditLog(errors.New("audit store unavailable")),
	)

	proxy := domain.Proxy{
		ID:       uuid.New(),
		Host:     "127.0.0.1",
		Port:     9056,
		IsActive: true,
	}
	if err := repository.CreateProxy(context.Background(), proxy); err != nil {
		t.Fatalf("CreateProxy() error = %v", err)
	}

	accountID := uuid.New()
	if err := repository.CreateAccount(context.Background(), domain.Account{
		ID:               accountID,
		Username:         "account-audit-fail-open-01",
		ProxyID:          proxy.ID,
		OperationalState: domain.AccountStateActive,
		IsActive:         true,
		IsReady:          true,
	}); err != nil {
		t.Fatalf("CreateAccount() error = %v", err)
	}

	err := repository.UpdateAccountState(
		context.Background(),
		accountID,
		domain.AccountStateQuarantined,
		false,
		true,
		false,
		true,
	)
	if err != nil {
		t.Fatalf("UpdateAccountState() must succeed even when audit fails, got error = %v", err)
	}

	got, err := repository.GetAccountWithProxy(context.Background(), accountID)
	if err != nil {
		t.Fatalf("GetAccountWithProxy() error = %v", err)
	}
	if got.Account.OperationalState != domain.AccountStateQuarantined {
		t.Fatalf("expected state %s, got %s", domain.AccountStateQuarantined, got.Account.OperationalState)
	}
	if got.Account.IsReady {
		t.Fatal("expected is_ready=false after state update")
	}
	if !got.Account.IsQuarantined || !got.Account.LimitReached {
		t.Fatalf("expected quarantine+limit flags to be set, got %+v", got.Account)
	}
}

func TestAccountRepositoryOperationalStateSnapshotReturnsCounts(t *testing.T) {
	pool := mustOpenTestPool(t)
	repository := postgresrepo.NewAccountRepository(pool, domain.DefaultRuntimeGuardrails())

	type testAccount struct {
		username string
		state    domain.AccountOperationalState
		isReady  bool
	}

	accounts := []testAccount{
		{username: "account-snapshot-active", state: domain.AccountStateActive, isReady: true},
		{username: "account-snapshot-busy", state: domain.AccountStateBusy, isReady: true},
		{username: "account-snapshot-invalid", state: domain.AccountStateInvalidSession, isReady: false},
		{username: "account-snapshot-quarantined", state: domain.AccountStateQuarantined, isReady: false},
		{username: "account-snapshot-restricted", state: domain.AccountStateRestricted, isReady: true},
	}

	for _, item := range accounts {
		proxy := domain.Proxy{
			ID:       uuid.New(),
			Host:     "127.0.0.1",
			Port:     9100 + time.Now().Nanosecond()%500,
			IsActive: true,
		}
		if err := repository.CreateProxy(context.Background(), proxy); err != nil {
			t.Fatalf("CreateProxy() error = %v", err)
		}

		if err := repository.CreateAccount(context.Background(), domain.Account{
			ID:               uuid.New(),
			Username:         item.username,
			ProxyID:          proxy.ID,
			OperationalState: item.state,
			IsActive:         true,
			IsReady:          item.isReady,
			IsQuarantined:    item.state == domain.AccountStateQuarantined,
			IsRestricted:     item.state == domain.AccountStateRestricted,
		}); err != nil {
			t.Fatalf("CreateAccount(%s) error = %v", item.username, err)
		}
	}

	snapshot, err := repository.OperationalStateSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OperationalStateSnapshot() error = %v", err)
	}

	expected := map[domain.AccountOperationalState]int64{
		domain.AccountStateActive:         1,
		domain.AccountStateBusy:           1,
		domain.AccountStateInvalidSession: 1,
		domain.AccountStateQuarantined:    1,
		domain.AccountStateRestricted:     1,
	}
	for state, want := range expected {
		if got := snapshot[state]; got != want {
			t.Fatalf("expected %s=%d, got %d", state, want, got)
		}
	}
}

func mustOpenTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("FOLLOWER_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("skipping integration test, FOLLOWER_TEST_POSTGRES_URL is not set")
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("skipping integration test, invalid FOLLOWER_TEST_POSTGRES_URL: %v", err)
	}
	dbName := strings.ToLower(poolConfig.ConnConfig.Database)
	if dbName == "" || (!strings.Contains(dbName, "test") && !strings.Contains(dbName, "automation")) {
		t.Skipf(
			"skipping integration test, database %q is not marked as test-safe (expected name containing test/automation)",
			poolConfig.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("skipping integration test, cannot create postgres pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping integration test, postgres unavailable: %v", err)
	}

	prepareTestDatabase(t, pool)
	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

func prepareTestDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		DROP TABLE IF EXISTS follow_results;
		DROP TABLE IF EXISTS tasks;
		DROP TABLE IF EXISTS audit_logs;
		DROP TABLE IF EXISTS account_sessions;
		DROP TABLE IF EXISTS accounts;
		DROP TABLE IF EXISTS proxies;

		CREATE TABLE IF NOT EXISTS proxies (
			id UUID PRIMARY KEY,
			host TEXT NOT NULL,
			port INTEGER NOT NULL CHECK (port > 0),
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS accounts (
			id UUID PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			proxy_id UUID REFERENCES proxies(id) ON DELETE RESTRICT,
			operational_state TEXT NOT NULL,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			is_ready BOOLEAN NOT NULL DEFAULT TRUE,
			is_quarantined BOOLEAN NOT NULL DEFAULT FALSE,
			is_restricted BOOLEAN NOT NULL DEFAULT FALSE,
			limit_reached BOOLEAN NOT NULL DEFAULT FALSE,
			active_execution_context_id TEXT NULL,
			credential_source TEXT NOT NULL DEFAULT 'manual',
			credential_ref TEXT NOT NULL DEFAULT 'manual://legacy',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_accounts_operational_state CHECK (
				operational_state IN ('active','busy','invalid_session','quarantined','restricted')
			),
			CONSTRAINT chk_accounts_credential_source CHECK (
				credential_source IN ('env','vault','file','manual')
			),
			CONSTRAINT chk_accounts_credential_ref_nonempty CHECK (
				NULLIF(BTRIM(credential_ref), '') IS NOT NULL
			)
		);

		CREATE INDEX IF NOT EXISTS idx_accounts_proxy_id
			ON accounts (proxy_id);

		CREATE INDEX IF NOT EXISTS idx_accounts_eligibility
			ON accounts (is_active, is_ready, is_quarantined, is_restricted, limit_reached);

		CREATE UNIQUE INDEX IF NOT EXISTS ux_accounts_active_execution_context
			ON accounts (active_execution_context_id)
			WHERE active_execution_context_id IS NOT NULL;

		CREATE TABLE IF NOT EXISTS account_sessions (
			account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
			revision BIGINT NOT NULL CHECK (revision > 0),
			status TEXT NOT NULL,
			object_key TEXT NOT NULL,
			error_code TEXT NULL,
			last_restored_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_account_sessions_status CHECK (
				status IN ('valid', 'invalid', 'unavailable')
			),
			CONSTRAINT chk_account_sessions_valid_without_error CHECK (
				NOT (status = 'valid' AND error_code IS NOT NULL)
			)
		);

		CREATE TABLE IF NOT EXISTS tasks (
			id UUID PRIMARY KEY,
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
			target_profile TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
			claimed_by TEXT NULL,
			claimed_at TIMESTAMPTZ NULL,
			started_at TIMESTAMPTZ NULL,
			finished_at TIMESTAMPTZ NULL,
			error_code TEXT NULL,
			result_reason TEXT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_tasks_status CHECK (
				status IN ('queued', 'running', 'success', 'retry', 'fail')
			),
			CONSTRAINT chk_tasks_lifecycle_consistency CHECK (
				(
					status = 'queued'
					AND attempt >= 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NULL
					AND claimed_at IS NULL
					AND started_at IS NULL
					AND finished_at IS NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status = 'running'
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status = 'success'
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NOT NULL
					AND error_code IS NULL
					AND result_reason IS NULL
				)
				OR (
					status IN ('retry', 'fail')
					AND attempt > 0
					AND NULLIF(BTRIM(target_profile), '') IS NOT NULL
					AND claimed_by IS NOT NULL
					AND BTRIM(claimed_by) <> ''
					AND claimed_at IS NOT NULL
					AND started_at IS NOT NULL
					AND finished_at IS NOT NULL
					AND (
						NULLIF(BTRIM(error_code), '') IS NOT NULL
						OR NULLIF(BTRIM(result_reason), '') IS NOT NULL
					)
				)
			),
			CONSTRAINT chk_tasks_lifecycle_temporal_order CHECK (
				(claimed_at IS NULL OR started_at IS NULL OR claimed_at <= started_at)
				AND (started_at IS NULL OR finished_at IS NULL OR started_at <= finished_at)
			)
		);

		CREATE INDEX IF NOT EXISTS idx_tasks_claim_next_queued_created_at_id
			ON tasks (created_at, id)
			WHERE status = 'queued';

		CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_id_account
			ON tasks (id, account_id);

		CREATE TABLE IF NOT EXISTS follow_results (
			task_id UUID NOT NULL,
			attempt INTEGER NOT NULL CHECK (attempt > 0),
			account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
			target_profile TEXT NOT NULL,
			outcome TEXT NOT NULL,
			verified BOOLEAN NOT NULL,
			verification_signal TEXT NOT NULL,
			verification_reason TEXT NULL,
			error_code TEXT NULL,
			screenshot_object_key TEXT NOT NULL,
			artifact_object_keys TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
			session_revision BIGINT NULL CHECK (session_revision > 0),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT pk_follow_results PRIMARY KEY (task_id, attempt),
			CONSTRAINT fk_follow_results_task_account FOREIGN KEY (task_id, account_id)
				REFERENCES tasks(id, account_id) ON DELETE CASCADE,
			CONSTRAINT chk_follow_results_target_profile_nonempty CHECK (
				NULLIF(BTRIM(target_profile), '') IS NOT NULL
			),
			CONSTRAINT chk_follow_results_outcome CHECK (
				outcome IN (
					'follow_completed',
					'follow_already_done',
					'follow_action_unavailable',
					'follow_target_unreachable',
					'follow_navigation_failed'
				)
			),
			CONSTRAINT chk_follow_results_signal CHECK (
				verification_signal IN (
					'follow_confirmed',
					'follow_already_done',
					'follow_action_unavailable',
					'follow_target_unreachable',
					'follow_navigation_failed',
					'follow_verify_failed'
				)
			),
			CONSTRAINT chk_follow_results_verified_vs_error CHECK (
				(verified = TRUE AND error_code IS NULL)
				OR (verified = FALSE AND NULLIF(BTRIM(COALESCE(error_code, '')), '') IS NOT NULL)
			),
			CONSTRAINT chk_follow_results_screenshot_nonempty CHECK (
				NULLIF(BTRIM(screenshot_object_key), '') IS NOT NULL
			),
			CONSTRAINT chk_follow_results_artifacts_nonempty CHECK (
				CARDINALITY(artifact_object_keys) > 0
			)
		);
	`)
	if err != nil {
		t.Fatalf("prepare test schema: %v", err)
	}
}

func proxyBindingDisabledGuardrails() domain.RuntimeGuardrails {
	guardrails := domain.DefaultRuntimeGuardrails()
	guardrails.RequireProxyBinding = false
	return guardrails
}
