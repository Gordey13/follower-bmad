package observability

import (
	"context"
	"sort"
	"strings"

	"follower/internal/audit"
)

const (
	EventTaskClaimed                     = "task.claimed"
	EventTaskStarted                     = "task.started"
	EventTaskSucceeded                   = "task.succeeded"
	EventTaskFailed                      = "task.failed"
	EventTaskClaimFailed                 = "task.claim_failed"
	EventTaskExecutionContextPrepareFail = "task.execution_context_prepare_failed"

	EventFollowWarmupStarted      = "follow.warmup.started"
	EventFollowWarmupSucceeded    = "follow.warmup.succeeded"
	EventFollowWarmupFailed       = "follow.warmup.failed"
	EventFollowExecutionStarted   = "follow.execution.started"
	EventFollowExecutionSucceeded = "follow.execution.succeeded"
	EventFollowExecutionFailed    = "follow.execution.failed"
	EventFollowActionClicked      = "follow.action.clicked"
	EventFollowVerifyStarted      = "follow.verify.started"
	EventFollowVerifySucceeded    = "follow.verify.succeeded"
	EventFollowVerifyFailed       = "follow.verify.failed"
	EventFollowFinalizeFailed     = "follow.finalize.failed"

	EventExecutionContextPrepared    = "execution_context.prepared"
	EventExecutionContextPrepareFail = "execution_context.prepare_failed"
	EventExecutionContextReleaseFail = "execution_context.release_failed"
	EventSessionRestoreStarted       = "session.restore_started"
	EventSessionRestored             = "session.restored"
	EventSessionRestoreFailed        = "session.restore_failed"
	EventBootstrapLoginStarted       = "bootstrap_login.started"
	EventBootstrapLoginSucceeded     = "bootstrap_login.succeeded"
	EventBootstrapLoginFailed        = "bootstrap_login.failed"
	EventSessionSaved                = "session.saved"
	EventFollowResultPersisted       = "follow.result.persisted"
	EventArtifactSaved               = "artifact.saved"
	EventFollowHistoryRead           = "follow.history.read"

	FieldComponent         = "component"
	FieldTaskID            = "task_id"
	FieldAccountID         = "account_id"
	FieldAttempt           = "attempt"
	FieldErrorCode         = "error_code"
	FieldDurationMS        = "duration_ms"
	FieldDiagnosticMessage = "diagnostic_message"
)

type LifecycleContext struct {
	Component  string
	TaskID     string
	AccountID  string
	Attempt    int
	ErrorCode  string
	DurationMS int64
}

type restoreLifecycleContextKey struct{}

type RestoreLifecycleContext struct {
	TaskID  string
	Attempt int
}

func WithRestoreLifecycleContext(ctx context.Context, taskID string, attempt int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := RestoreLifecycleContext{
		TaskID:  strings.TrimSpace(taskID),
		Attempt: attempt,
	}
	if normalized.Attempt < 0 {
		normalized.Attempt = 0
	}
	return context.WithValue(ctx, restoreLifecycleContextKey{}, normalized)
}

func RestoreLifecycleContextFrom(ctx context.Context) RestoreLifecycleContext {
	if ctx == nil {
		return RestoreLifecycleContext{TaskID: "n/a", Attempt: 0}
	}
	value, ok := ctx.Value(restoreLifecycleContextKey{}).(RestoreLifecycleContext)
	if !ok {
		return RestoreLifecycleContext{TaskID: "n/a", Attempt: 0}
	}
	if strings.TrimSpace(value.TaskID) == "" {
		value.TaskID = "n/a"
	}
	if value.Attempt < 0 {
		value.Attempt = 0
	}
	return value
}

func LifecycleAttrs(context LifecycleContext, extras ...any) []any {
	normalized := normalizeLifecycleContext(context)
	attrs := []any{
		FieldComponent, normalized.Component,
		FieldTaskID, normalized.TaskID,
		FieldAccountID, normalized.AccountID,
		FieldAttempt, normalized.Attempt,
		FieldErrorCode, normalized.ErrorCode,
		FieldDurationMS, normalized.DurationMS,
	}
	if len(extras) > 0 {
		attrs = append(attrs, extras...)
	}
	return attrs
}

func ErrorLifecycleAttrs(context LifecycleContext, diagnosticMessage string, extras ...any) []any {
	attrs := LifecycleAttrs(context, extras...)
	return append(attrs, FieldDiagnosticMessage, SanitizeDiagnosticMessage(diagnosticMessage))
}

func SafeStringAttrs(fields map[string]string) []any {
	sanitized := audit.SanitizeDiagnosticFields(fields)
	if len(sanitized) == 0 {
		return []any{}
	}

	keys := make([]string, 0, len(sanitized))
	for key := range sanitized {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	attrs := make([]any, 0, len(sanitized)*2)
	for _, key := range keys {
		attrs = append(attrs, key, sanitized[key])
	}
	return attrs
}

func SanitizeDiagnosticMessage(message string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
	if normalized == "" {
		return "diagnostic details unavailable"
	}

	lowered := strings.ToLower(normalized)
	for _, token := range sensitiveTokens() {
		if strings.Contains(lowered, token) {
			return "diagnostic message redacted"
		}
	}

	if len(normalized) > 256 {
		return normalized[:256]
	}
	return normalized
}

func normalizeLifecycleContext(context LifecycleContext) LifecycleContext {
	normalized := context

	if strings.TrimSpace(normalized.Component) == "" {
		normalized.Component = "unknown_component"
	}
	if strings.TrimSpace(normalized.TaskID) == "" {
		normalized.TaskID = "n/a"
	}
	if strings.TrimSpace(normalized.AccountID) == "" {
		normalized.AccountID = "n/a"
	}
	if normalized.Attempt < 0 {
		normalized.Attempt = 0
	}
	if strings.TrimSpace(normalized.ErrorCode) == "" {
		normalized.ErrorCode = "internal_error"
	}
	if normalized.DurationMS < 0 {
		normalized.DurationMS = 0
	}

	return normalized
}

func sensitiveTokens() []string {
	return []string{
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
}
