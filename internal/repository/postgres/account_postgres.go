package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"follower/internal/audit"
	"follower/internal/domain"
	"follower/internal/stackerr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AccountPostgresRepository struct {
	pool       *pgxpool.Pool
	guardrails domain.RuntimeGuardrails
	auditLog   *audit.Log
}

func NewAccountRepository(
	pool *pgxpool.Pool,
	guardrails domain.RuntimeGuardrails,
	auditLog ...*audit.Log,
) *AccountPostgresRepository {
	var logger *audit.Log
	if len(auditLog) > 0 {
		logger = auditLog[0]
	}

	return &AccountPostgresRepository{
		pool:       pool,
		guardrails: guardrails.Normalized(),
		auditLog:   logger,
	}
}

func (r *AccountPostgresRepository) CreateProxy(ctx context.Context, proxy domain.Proxy) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO proxies (
			id, host, port, is_active
		) VALUES ($1, $2, $3, $4)
	`, proxy.ID, proxy.Host, proxy.Port, proxy.IsActive)
	return stackerr.WithStack(err)
}

func (r *AccountPostgresRepository) CreateAccount(ctx context.Context, account domain.Account) error {
	if !account.OperationalState.IsValid() {
		return domain.NewDomainError(
			domain.ErrorCodeInvalidOperationalState,
			fmt.Sprintf("invalid account operational state: %s", account.OperationalState),
		)
	}
	account.CredentialSource = normalizeCredentialSource(account.CredentialSource)
	account.CredentialRef = normalizeCredentialRef(account.CredentialRef)

	_, err := r.pool.Exec(ctx, `
		INSERT INTO accounts (
			id,
			username,
			proxy_id,
			credential_source,
			credential_ref,
			operational_state,
			is_active,
			is_ready,
			is_quarantined,
			is_restricted,
			limit_reached,
			active_execution_context_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`,
		account.ID,
		account.Username,
		nullableUUID(account.ProxyID),
		account.CredentialSource,
		account.CredentialRef,
		account.OperationalState,
		account.IsActive,
		account.IsReady,
		account.IsQuarantined,
		account.IsRestricted,
		account.LimitReached,
		nullableString(account.ActiveExecutionContextID),
	)
	return stackerr.WithStack(err)
}

func (r *AccountPostgresRepository) GetAccountWithProxy(ctx context.Context, accountID uuid.UUID) (domain.AccountWithProxy, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			a.id,
			a.username,
			a.proxy_id,
			a.credential_source,
			a.credential_ref,
			a.operational_state,
			a.is_active,
			a.is_ready,
			a.is_quarantined,
			a.is_restricted,
			a.limit_reached,
			COALESCE(a.active_execution_context_id, ''),
			a.created_at,
			a.updated_at,
			p.id,
			p.host,
			p.port,
			p.is_active,
			p.created_at,
			p.updated_at
		FROM accounts a
		LEFT JOIN proxies p ON p.id = a.proxy_id
		WHERE a.id = $1
	`, accountID)

	accountWithProxy, err := scanAccountWithOptionalProxy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccountWithProxy{}, domain.NewDomainError(
			domain.ErrorCodeAccountNotFound,
			fmt.Sprintf("account %s not found", accountID.String()),
		)
	}
	if err != nil {
		return domain.AccountWithProxy{}, stackerr.WithStack(err)
	}

	return accountWithProxy.toAccountWithProxy(), nil
}

func (r *AccountPostgresRepository) OperationalStateSnapshot(
	ctx context.Context,
) (map[domain.AccountOperationalState]int64, error) {
	snapshot := map[domain.AccountOperationalState]int64{
		domain.AccountStateActive:         0,
		domain.AccountStateBusy:           0,
		domain.AccountStateInvalidSession: 0,
		domain.AccountStateQuarantined:    0,
		domain.AccountStateRestricted:     0,
	}

	rows, err := r.pool.Query(ctx, `
		SELECT operational_state, COUNT(*)::BIGINT
		FROM accounts
		WHERE operational_state IN ('active', 'busy', 'invalid_session', 'quarantined', 'restricted')
		GROUP BY operational_state
	`)
	if err != nil {
		return nil, stackerr.WithStack(err)
	}
	defer rows.Close()

	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, stackerr.WithStack(err)
		}

		normalizedState := domain.AccountOperationalState(state)
		if !normalizedState.IsValid() {
			continue
		}
		snapshot[normalizedState] = count
	}

	if err := rows.Err(); err != nil {
		return nil, stackerr.WithStack(err)
	}

	return snapshot, nil
}

func (r *AccountPostgresRepository) UpdateAccountState(
	ctx context.Context,
	accountID uuid.UUID,
	state domain.AccountOperationalState,
	isReady bool,
	isQuarantined bool,
	isRestricted bool,
	limitReached bool,
) error {
	if !state.IsValid() {
		return domain.NewDomainError(
			domain.ErrorCodeInvalidOperationalState,
			fmt.Sprintf("invalid account operational state: %s", state),
		)
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE accounts
		SET operational_state = $2,
			is_ready = $3,
			is_quarantined = $4,
			is_restricted = $5,
			limit_reached = $6,
			updated_at = NOW()
		WHERE id = $1
	`, accountID, state, isReady, isQuarantined, isRestricted, limitReached)
	if err != nil {
		return stackerr.WithStack(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NewDomainError(
			domain.ErrorCodeAccountNotFound,
			fmt.Sprintf("account %s not found", accountID.String()),
		)
	}
	if r.auditLog != nil {
		_, auditErr := r.auditLog.Record(ctx, audit.Event{
			Action:        "account.state_changed",
			TargetType:    "account",
			TargetID:      accountID.String(),
			ChangeSummary: fmt.Sprintf("account operational state changed to %s", state),
			DiagnosticFields: map[string]string{
				"operational_state": string(state),
				"is_ready":          strconv.FormatBool(isReady),
				"is_quarantined":    strconv.FormatBool(isQuarantined),
				"is_restricted":     strconv.FormatBool(isRestricted),
				"limit_reached":     strconv.FormatBool(limitReached),
			},
		})
		if auditErr != nil {
			// Domain write is already committed; audit is best-effort and must not break state transitions.
		}
	}

	return nil
}

func (r *AccountPostgresRepository) CheckEligibility(
	ctx context.Context,
	accountID uuid.UUID,
) (domain.EligibilityDecision, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			a.id,
			a.username,
			a.proxy_id,
			a.credential_source,
			a.credential_ref,
			a.operational_state,
			a.is_active,
			a.is_ready,
			a.is_quarantined,
			a.is_restricted,
			a.limit_reached,
			COALESCE(a.active_execution_context_id, ''),
			a.created_at,
			a.updated_at,
			p.id,
			p.host,
			p.port,
			p.is_active,
			p.created_at,
			p.updated_at
		FROM accounts a
		LEFT JOIN proxies p ON p.id = a.proxy_id
		WHERE a.id = $1
	`, accountID)

	accountWithProxy, err := scanAccountWithOptionalProxy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EligibilityDecision{
			Eligible:   false,
			Outcome:    domain.EligibilityOutcomeExcluded,
			ReasonCode: domain.ErrorCodeAccountNotFound,
		}, nil
	}
	if err != nil {
		return domain.EligibilityDecision{}, stackerr.WithStack(err)
	}

	return domain.EvaluateAccountEligibilityWithGuardrails(
		accountWithProxy.Account,
		accountWithProxy.Proxy,
		r.guardrails,
	), nil
}

func (r *AccountPostgresRepository) ClaimAccount(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) (domain.Account, error) {
	if executionContextID == "" {
		return domain.Account{}, domain.NewDomainError(
			domain.ErrorCodeInvalidExecutionContext,
			"execution context id must not be empty",
		)
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}
	defer tx.Rollback(ctx)

	accountWithProxy, err := selectAccountWithOptionalProxyForUpdate(ctx, tx, accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.NewDomainError(
			domain.ErrorCodeAccountNotFound,
			fmt.Sprintf("account %s not found", accountID.String()),
		)
	}
	if err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}

	if accountWithProxy.Account.ActiveExecutionContextID == executionContextID {
		if accountWithProxy.Account.OperationalState != domain.AccountStateBusy {
			row := tx.QueryRow(ctx, `
				UPDATE accounts
				SET operational_state = $2,
					updated_at = NOW()
				WHERE id = $1
			RETURNING
				id,
				username,
				proxy_id,
				credential_source,
				credential_ref,
				operational_state,
					is_active,
					is_ready,
					is_quarantined,
					is_restricted,
					limit_reached,
					COALESCE(active_execution_context_id, ''),
					created_at,
					updated_at
			`, accountID, domain.AccountStateBusy)
			claimedAccount, scanErr := scanAccount(row)
			if scanErr != nil {
				return domain.Account{}, stackerr.WithStack(scanErr)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return domain.Account{}, stackerr.WithStack(commitErr)
			}
			r.recordLifecycleAuditClaim(ctx, accountID, executionContextID, claimedAccount.OperationalState)
			return claimedAccount, nil
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			return domain.Account{}, stackerr.WithStack(commitErr)
		}
		return accountWithProxy.Account, nil
	}

	decision := domain.EvaluateAccountEligibilityWithGuardrails(
		accountWithProxy.Account,
		accountWithProxy.Proxy,
		r.guardrails,
	)
	if !decision.Eligible {
		return domain.Account{}, domain.NewDomainError(
			decision.ReasonCode,
			fmt.Sprintf("account %s is not eligible", accountID.String()),
		)
	}

	if err := domain.EnsureSingleActiveExecution(accountWithProxy.Account, executionContextID); err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}

	row := tx.QueryRow(ctx, `
		UPDATE accounts
		SET active_execution_context_id = $2,
			operational_state = $3,
			updated_at = NOW()
		WHERE id = $1
	RETURNING
		id,
		username,
		proxy_id,
		credential_source,
		credential_ref,
		operational_state,
			is_active,
			is_ready,
			is_quarantined,
			is_restricted,
			limit_reached,
			COALESCE(active_execution_context_id, ''),
			created_at,
			updated_at
	`, accountID, executionContextID, domain.AccountStateBusy)

	claimedAccount, err := scanAccount(row)
	if err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}
	r.recordLifecycleAuditClaim(ctx, accountID, executionContextID, claimedAccount.OperationalState)

	return claimedAccount, nil
}

func (r *AccountPostgresRepository) ReleaseAccount(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
) error {
	row := r.pool.QueryRow(ctx, `
		UPDATE accounts
		SET active_execution_context_id = NULL,
			operational_state = CASE
				WHEN is_quarantined THEN 'quarantined'
				WHEN is_restricted THEN 'restricted'
				WHEN NOT is_active THEN 'restricted'
				WHEN NOT is_ready THEN 'invalid_session'
				ELSE 'active'
			END,
			updated_at = NOW()
		WHERE id = $1
		AND active_execution_context_id = $2
		RETURNING operational_state
	`, accountID, executionContextID)

	var operationalState string
	if err := row.Scan(&operationalState); errors.Is(err, pgx.ErrNoRows) {
		return domain.NewDomainError(
			domain.ErrorCodeAccountContextMismatch,
			fmt.Sprintf("account %s is not claimed by execution context %s", accountID.String(), executionContextID),
		)
	} else if err != nil {
		return stackerr.WithStack(err)
	}

	r.recordLifecycleAuditRelease(
		ctx,
		accountID,
		executionContextID,
		domain.AccountOperationalState(operationalState),
	)

	return nil
}

func selectAccountWithOptionalProxyForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	accountID uuid.UUID,
) (accountWithOptionalProxy, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			a.id,
			a.username,
			a.proxy_id,
			a.credential_source,
			a.credential_ref,
			a.operational_state,
			a.is_active,
			a.is_ready,
			a.is_quarantined,
			a.is_restricted,
			a.limit_reached,
			COALESCE(a.active_execution_context_id, ''),
			a.created_at,
			a.updated_at,
			p.id,
			p.host,
			p.port,
			p.is_active,
			p.created_at,
			p.updated_at
		FROM accounts a
		LEFT JOIN proxies p ON p.id = a.proxy_id
		WHERE a.id = $1
		FOR UPDATE OF a
	`, accountID)
	return scanAccountWithOptionalProxy(row)
}

func scanAccount(row pgx.Row) (domain.Account, error) {
	var account domain.Account
	var operationalState string
	var credentialSource string
	var credentialRef string
	var proxyID *uuid.UUID
	err := row.Scan(
		&account.ID,
		&account.Username,
		&proxyID,
		&credentialSource,
		&credentialRef,
		&operationalState,
		&account.IsActive,
		&account.IsReady,
		&account.IsQuarantined,
		&account.IsRestricted,
		&account.LimitReached,
		&account.ActiveExecutionContextID,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		return domain.Account{}, stackerr.WithStack(err)
	}
	if proxyID != nil {
		account.ProxyID = *proxyID
	}
	account.CredentialSource = normalizeCredentialSource(domain.CredentialSource(credentialSource))
	account.CredentialRef = normalizeCredentialRef(credentialRef)
	account.OperationalState = domain.AccountOperationalState(operationalState)

	return account, nil
}

func scanAccountWithProxy(row pgx.Row) (domain.AccountWithProxy, error) {
	var accountWithProxy domain.AccountWithProxy
	var operationalState string
	var credentialSource string
	var credentialRef string
	err := row.Scan(
		&accountWithProxy.Account.ID,
		&accountWithProxy.Account.Username,
		&accountWithProxy.Account.ProxyID,
		&credentialSource,
		&credentialRef,
		&operationalState,
		&accountWithProxy.Account.IsActive,
		&accountWithProxy.Account.IsReady,
		&accountWithProxy.Account.IsQuarantined,
		&accountWithProxy.Account.IsRestricted,
		&accountWithProxy.Account.LimitReached,
		&accountWithProxy.Account.ActiveExecutionContextID,
		&accountWithProxy.Account.CreatedAt,
		&accountWithProxy.Account.UpdatedAt,
		&accountWithProxy.Proxy.ID,
		&accountWithProxy.Proxy.Host,
		&accountWithProxy.Proxy.Port,
		&accountWithProxy.Proxy.IsActive,
		&accountWithProxy.Proxy.CreatedAt,
		&accountWithProxy.Proxy.UpdatedAt,
	)
	if err != nil {
		return domain.AccountWithProxy{}, stackerr.WithStack(err)
	}
	accountWithProxy.Account.CredentialSource = normalizeCredentialSource(domain.CredentialSource(credentialSource))
	accountWithProxy.Account.CredentialRef = normalizeCredentialRef(credentialRef)
	accountWithProxy.Account.OperationalState = domain.AccountOperationalState(operationalState)

	return accountWithProxy, nil
}

type accountWithOptionalProxy struct {
	Account domain.Account
	Proxy   *domain.Proxy
}

func scanAccountWithOptionalProxy(row pgx.Row) (accountWithOptionalProxy, error) {
	var result accountWithOptionalProxy
	var operationalState string
	var credentialSource string
	var credentialRef string
	var accountProxyID *uuid.UUID
	var proxyID *uuid.UUID
	var proxyHost *string
	var proxyPort *int
	var proxyIsActive *bool
	var proxyCreatedAt *time.Time
	var proxyUpdatedAt *time.Time

	err := row.Scan(
		&result.Account.ID,
		&result.Account.Username,
		&accountProxyID,
		&credentialSource,
		&credentialRef,
		&operationalState,
		&result.Account.IsActive,
		&result.Account.IsReady,
		&result.Account.IsQuarantined,
		&result.Account.IsRestricted,
		&result.Account.LimitReached,
		&result.Account.ActiveExecutionContextID,
		&result.Account.CreatedAt,
		&result.Account.UpdatedAt,
		&proxyID,
		&proxyHost,
		&proxyPort,
		&proxyIsActive,
		&proxyCreatedAt,
		&proxyUpdatedAt,
	)
	if err != nil {
		return accountWithOptionalProxy{}, stackerr.WithStack(err)
	}
	if accountProxyID != nil {
		result.Account.ProxyID = *accountProxyID
	}
	result.Account.CredentialSource = normalizeCredentialSource(domain.CredentialSource(credentialSource))
	result.Account.CredentialRef = normalizeCredentialRef(credentialRef)
	result.Account.OperationalState = domain.AccountOperationalState(operationalState)

	if proxyID != nil && proxyHost != nil && proxyPort != nil && proxyIsActive != nil && proxyCreatedAt != nil && proxyUpdatedAt != nil {
		result.Proxy = &domain.Proxy{
			ID:        *proxyID,
			Host:      *proxyHost,
			Port:      *proxyPort,
			IsActive:  *proxyIsActive,
			CreatedAt: *proxyCreatedAt,
			UpdatedAt: *proxyUpdatedAt,
		}
	}

	return result, nil
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func nullableUUID(value uuid.UUID) interface{} {
	if value == uuid.Nil {
		return nil
	}

	return value
}

func normalizeCredentialSource(source domain.CredentialSource) domain.CredentialSource {
	return domain.NormalizeCredentialSource(source)
}

func normalizeCredentialRef(reference string) string {
	normalized := strings.TrimSpace(reference)
	if normalized == "" {
		return "manual://legacy"
	}
	return normalized
}

func (a accountWithOptionalProxy) toAccountWithProxy() domain.AccountWithProxy {
	accountWithProxy := domain.AccountWithProxy{
		Account: a.Account,
	}
	if a.Proxy != nil {
		accountWithProxy.Proxy = *a.Proxy
	}

	return accountWithProxy
}

func (r *AccountPostgresRepository) recordLifecycleAuditClaim(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
	state domain.AccountOperationalState,
) {
	if r.auditLog == nil {
		return
	}

	_, _ = r.auditLog.Record(ctx, audit.Event{
		Action:        "account.claimed",
		TargetType:    "account",
		TargetID:      accountID.String(),
		ChangeSummary: "account claimed for active execution",
		DiagnosticFields: map[string]string{
			"execution_context_id": executionContextID,
			"operational_state":    string(state),
		},
	})
}

func (r *AccountPostgresRepository) recordLifecycleAuditRelease(
	ctx context.Context,
	accountID uuid.UUID,
	executionContextID string,
	state domain.AccountOperationalState,
) {
	if r.auditLog == nil {
		return
	}

	_, _ = r.auditLog.Record(ctx, audit.Event{
		Action:        "account.released",
		TargetType:    "account",
		TargetID:      accountID.String(),
		ChangeSummary: "account released from active execution",
		DiagnosticFields: map[string]string{
			"execution_context_id": executionContextID,
			"operational_state":    string(state),
		},
	})
}
