package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	policyBlockStart = "-- BMAD_EDIT_POLICY_START"
	policyBlockEnd   = "-- BMAD_EDIT_POLICY_END"
	rowsBlockStart   = "-- BMAD_EDIT_ROWS_START"
	rowsBlockEnd     = "-- BMAD_EDIT_ROWS_END"
)

func TestMigration000011AddAccountCredentialRefsUpAndDown(t *testing.T) {
	pool := mustOpenTestPool(t)

	ctx := context.Background()
	_, err := pool.Exec(ctx, `
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
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_accounts_operational_state CHECK (
				operational_state IN ('active','busy','invalid_session','quarantined','restricted')
			)
		);
	`)
	if err != nil {
		t.Fatalf("prepare baseline schema: %v", err)
	}

	upSQL := mustReadProjectFile(t, "db", "migrations", "000011_add_account_credentials_ref.up.sql")
	if _, err := pool.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 000011 up migration: %v", err)
	}

	var columnCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'accounts'
		  AND column_name IN ('credential_source', 'credential_ref')
	`).Scan(&columnCount); err != nil {
		t.Fatalf("query credential columns: %v", err)
	}
	if columnCount != 2 {
		t.Fatalf("expected both credential columns to exist, got %d", columnCount)
	}

	accountID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (
			id,
			username,
			operational_state,
			is_active,
			is_ready,
			is_quarantined,
			is_restricted,
			limit_reached,
			active_execution_context_id
		) VALUES ($1, $2, 'active', TRUE, TRUE, FALSE, FALSE, FALSE, NULL)
	`, accountID, "migration-credential-check"); err != nil {
		t.Fatalf("insert account with migration defaults: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE accounts SET credential_source = 'invalid' WHERE id = $1
	`, accountID); err == nil {
		t.Fatal("expected credential_source check to reject invalid value")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE accounts SET credential_ref = '   ' WHERE id = $1
	`, accountID); err == nil {
		t.Fatal("expected credential_ref check to reject blank value")
	}

	downSQL := mustReadProjectFile(t, "db", "migrations", "000011_add_account_credentials_ref.down.sql")
	if _, err := pool.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply 000011 down migration: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'accounts'
		  AND column_name IN ('credential_source', 'credential_ref')
	`).Scan(&columnCount); err != nil {
		t.Fatalf("query credential columns after down migration: %v", err)
	}
	if columnCount != 0 {
		t.Fatalf("expected credential columns to be removed by down migration, got %d", columnCount)
	}
}

func TestInsertAccountsScriptUpsertAndIdempotency(t *testing.T) {
	pool := mustOpenTestPool(t)
	ctx := context.Background()

	proxyID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO proxies (id, host, port, is_active)
		VALUES ($1, '127.0.0.1', 9150, TRUE)
	`, proxyID)
	if err != nil {
		t.Fatalf("insert proxy: %v", err)
	}

	baseScript := mustReadProjectFile(t, "scripts", "insert-accounts.sql")

	firstRows := fmt.Sprintf(
		"('demo-insert-accounts-01', 'env', 'env://FOLLOWER_DEMO_ACCOUNT_01', TRUE, TRUE, 'active', '%s'::UUID)",
		proxyID,
	)
	firstRunScript := withEditableRows(withProxyPolicy(baseScript, true), firstRows)
	if _, err := pool.Exec(ctx, firstRunScript); err != nil {
		t.Fatalf("first script run: %v", err)
	}

	var firstID uuid.UUID
	var firstSource string
	var firstRef string
	var firstIsReady bool
	if err := pool.QueryRow(ctx, `
		SELECT id, credential_source, credential_ref, is_ready
		FROM accounts
		WHERE username = 'demo-insert-accounts-01'
	`).Scan(&firstID, &firstSource, &firstRef, &firstIsReady); err != nil {
		t.Fatalf("select inserted account: %v", err)
	}
	if firstSource != "env" || firstRef != "env://FOLLOWER_DEMO_ACCOUNT_01" || !firstIsReady {
		t.Fatalf("unexpected first insert payload: source=%s ref=%s is_ready=%t", firstSource, firstRef, firstIsReady)
	}

	secondRows := fmt.Sprintf(
		"('demo-insert-accounts-01', 'vault', 'vault://demo/account/01', TRUE, FALSE, 'active', '%s'::UUID)",
		proxyID,
	)
	secondRunScript := withEditableRows(withProxyPolicy(baseScript, true), secondRows)
	if _, err := pool.Exec(ctx, secondRunScript); err != nil {
		t.Fatalf("second script run: %v", err)
	}

	var (
		accountCount  int
		secondID      uuid.UUID
		secondSource  string
		secondRef     string
		secondIsReady bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)::INT
		FROM accounts
		WHERE username = 'demo-insert-accounts-01'
	`).Scan(&accountCount); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accountCount != 1 {
		t.Fatalf("expected idempotent upsert with a single row, got %d", accountCount)
	}

	if err := pool.QueryRow(ctx, `
		SELECT id, credential_source, credential_ref, is_ready
		FROM accounts
		WHERE username = 'demo-insert-accounts-01'
	`).Scan(&secondID, &secondSource, &secondRef, &secondIsReady); err != nil {
		t.Fatalf("select updated account: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected stable account id across upserts, got %s then %s", firstID, secondID)
	}
	if secondSource != "vault" || secondRef != "vault://demo/account/01" || secondIsReady {
		t.Fatalf("unexpected upsert payload: source=%s ref=%s is_ready=%t", secondSource, secondRef, secondIsReady)
	}
}

func TestInsertAccountsScriptRejectsInvalidInput(t *testing.T) {
	pool := mustOpenTestPool(t)
	ctx := context.Background()

	proxyID := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO proxies (id, host, port, is_active)
		VALUES ($1, '127.0.0.1', 9151, TRUE)
	`, proxyID)
	if err != nil {
		t.Fatalf("insert proxy: %v", err)
	}

	baseScript := mustReadProjectFile(t, "scripts", "insert-accounts.sql")

	testCases := []struct {
		name          string
		proxyRequired bool
		rowSQL        string
		errorHint     string
	}{
		{
			name:          "invalid credential source",
			proxyRequired: true,
			rowSQL: fmt.Sprintf(
				"('demo-invalid-source-01', 'bad_source', 'env://FOLLOWER_DEMO_ACCOUNT_02', TRUE, TRUE, 'active', '%s'::UUID)",
				proxyID,
			),
			errorHint: "credential_source",
		},
		{
			name:          "empty credential ref",
			proxyRequired: true,
			rowSQL: fmt.Sprintf(
				"('demo-empty-ref-01', 'env', '   ', TRUE, TRUE, 'active', '%s'::UUID)",
				proxyID,
			),
			errorHint: "credential_ref",
		},
		{
			name:          "invalid operational state",
			proxyRequired: true,
			rowSQL: fmt.Sprintf(
				"('demo-invalid-state-01', 'env', 'env://FOLLOWER_DEMO_ACCOUNT_03', TRUE, TRUE, 'broken', '%s'::UUID)",
				proxyID,
			),
			errorHint: "operational_state",
		},
		{
			name:          "proxy required but missing",
			proxyRequired: true,
			rowSQL:        "('demo-missing-proxy-01', 'env', 'env://FOLLOWER_DEMO_ACCOUNT_04', TRUE, TRUE, 'active', NULL::UUID)",
			errorHint:     "proxy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sqlText := withEditableRows(withProxyPolicy(baseScript, tc.proxyRequired), tc.rowSQL)
			_, runErr := pool.Exec(ctx, sqlText)
			if runErr == nil {
				t.Fatal("expected script execution to fail")
			}
			if !strings.Contains(strings.ToLower(runErr.Error()), strings.ToLower(tc.errorHint)) {
				t.Fatalf("expected error to mention %q, got: %v", tc.errorHint, runErr)
			}
		})
	}
}

func withProxyPolicy(sqlText string, required bool) string {
	value := "FALSE"
	if required {
		value = "TRUE"
	}
	return replaceEditableBlock(
		sqlText,
		policyBlockStart,
		policyBlockEnd,
		fmt.Sprintf("SELECT %s::BOOLEAN AS proxy_binding_required", value),
	)
}

func withEditableRows(sqlText string, rowsSQL string) string {
	return replaceEditableBlock(sqlText, rowsBlockStart, rowsBlockEnd, rowsSQL)
}

func replaceEditableBlock(sqlText string, startMarker string, endMarker string, replacement string) string {
	startIdx := strings.Index(sqlText, startMarker)
	endIdx := strings.Index(sqlText, endMarker)
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return sqlText
	}

	startContent := startIdx + len(startMarker)
	return sqlText[:startContent] + "\n" + replacement + "\n" + sqlText[endIdx:]
}

func mustReadProjectFile(t *testing.T, parts ...string) string {
	t.Helper()

	pathParts := append([]string{"..", "..", ".."}, parts...)
	filePath := filepath.Join(pathParts...)

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file %s: %v", filePath, err)
	}
	return string(content)
}
