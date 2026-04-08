package observability

import (
	"strings"

	"follower/internal/domain"

	"github.com/prometheus/client_golang/prometheus"
)

type TaskLifecycleMetrics struct {
	claimedTotal          prometheus.Counter
	startedTotal          prometheus.Counter
	completedTotal        *prometheus.CounterVec
	errorTotal            *prometheus.CounterVec
	errorCodeTotal        *prometheus.CounterVec
	queueTotal            *prometheus.GaugeVec
	executionOutcomeTotal *prometheus.CounterVec
	dependencyReady       *prometheus.GaugeVec
	accountOperational    *prometheus.GaugeVec
	sessionStatus         *prometheus.GaugeVec
}

func NewTaskLifecycleMetrics(registry *prometheus.Registry) *TaskLifecycleMetrics {
	metrics := &TaskLifecycleMetrics{
		claimedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "follower_task_claimed_total",
			Help: "Total number of tasks successfully claimed by workers.",
		}),
		startedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "follower_task_started_total",
			Help: "Total number of tasks started in worker execution lifecycle.",
		}),
		completedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_task_completed_total",
			Help: "Total number of task completions partitioned by final status.",
		}, []string{"status"}),
		errorTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_task_error_total",
			Help: "Total number of task lifecycle errors partitioned by stage.",
		}, []string{"stage"}),
		errorCodeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_task_error_code_total",
			Help: "Total number of task lifecycle errors partitioned by stage and error code.",
		}, []string{"stage", "error_code"}),
		queueTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "follower_task_queue_total",
			Help: "Snapshot of task totals partitioned by lifecycle status.",
		}, []string{"status"}),
		executionOutcomeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_execution_outcome_total",
			Help: "Total number of follow execution outcomes partitioned by outcome.",
		}, []string{"outcome"}),
		dependencyReady: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "follower_dependency_ready",
			Help: "Dependency readiness state where 1=ready and 0=not-ready.",
		}, []string{"dependency"}),
		accountOperational: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "follower_account_operational_total",
			Help: "Snapshot of account totals partitioned by operational state.",
		}, []string{"state"}),
		sessionStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "follower_session_status_total",
			Help: "Snapshot of session totals partitioned by status.",
		}, []string{"status"}),
	}

	if registry != nil {
		registry.MustRegister(
			metrics.claimedTotal,
			metrics.startedTotal,
			metrics.completedTotal,
			metrics.errorTotal,
			metrics.errorCodeTotal,
			metrics.queueTotal,
			metrics.executionOutcomeTotal,
			metrics.dependencyReady,
			metrics.accountOperational,
			metrics.sessionStatus,
		)
	}

	return metrics
}

func (m *TaskLifecycleMetrics) RecordClaimed() {
	if m == nil {
		return
	}
	m.claimedTotal.Inc()
}

func (m *TaskLifecycleMetrics) RecordStarted() {
	if m == nil {
		return
	}
	m.startedTotal.Inc()
}

func (m *TaskLifecycleMetrics) RecordCompleted(status string) {
	if m == nil {
		return
	}

	m.completedTotal.WithLabelValues(normalizeTaskStatusLabel(status)).Inc()
}

func (m *TaskLifecycleMetrics) RecordError(stage string) {
	if m == nil {
		return
	}

	m.errorTotal.WithLabelValues(normalizeStageLabel(stage)).Inc()
}

func (m *TaskLifecycleMetrics) RecordErrorCode(stage string, errorCode string) {
	if m == nil {
		return
	}

	m.errorCodeTotal.WithLabelValues(
		normalizeStageLabel(stage),
		normalizeErrorCodeLabel(errorCode),
	).Inc()
}

func (m *TaskLifecycleMetrics) RecordExecutionOutcome(outcome string) {
	if m == nil {
		return
	}

	m.executionOutcomeTotal.WithLabelValues(normalizeExecutionOutcomeLabel(outcome)).Inc()
}

func (m *TaskLifecycleMetrics) RecordDependencyReady(dependency string, ready bool) {
	if m == nil {
		return
	}

	value := 0.0
	if ready {
		value = 1
	}
	m.dependencyReady.WithLabelValues(normalizeDependencyLabel(dependency)).Set(value)
}

func (m *TaskLifecycleMetrics) SetTaskQueueSnapshot(snapshot map[domain.TaskStatus]int64) {
	if m == nil {
		return
	}

	for _, status := range taskSnapshotStatuses {
		value := int64(0)
		if snapshot != nil {
			value = snapshot[status]
		}
		if value < 0 {
			value = 0
		}
		m.queueTotal.WithLabelValues(string(status)).Set(float64(value))
	}
}

func (m *TaskLifecycleMetrics) SetAccountOperationalSnapshot(
	snapshot map[domain.AccountOperationalState]int64,
) {
	if m == nil {
		return
	}

	for _, state := range accountSnapshotStates {
		value := int64(0)
		if snapshot != nil {
			value = snapshot[state]
		}
		if value < 0 {
			value = 0
		}
		m.accountOperational.WithLabelValues(string(state)).Set(float64(value))
	}
}

func (m *TaskLifecycleMetrics) SetSessionStatusSnapshot(snapshot map[domain.SessionStatus]int64) {
	if m == nil {
		return
	}

	for _, status := range sessionSnapshotStatuses {
		value := int64(0)
		if snapshot != nil {
			value = snapshot[status]
		}
		if value < 0 {
			value = 0
		}
		m.sessionStatus.WithLabelValues(string(status)).Set(float64(value))
	}
}

var taskSnapshotStatuses = []domain.TaskStatus{
	domain.TaskStatusQueued,
	domain.TaskStatusRunning,
	domain.TaskStatusSuccess,
	domain.TaskStatusRetry,
	domain.TaskStatusFail,
}

var accountSnapshotStates = []domain.AccountOperationalState{
	domain.AccountStateActive,
	domain.AccountStateBusy,
	domain.AccountStateInvalidSession,
	domain.AccountStateQuarantined,
	domain.AccountStateRestricted,
}

var sessionSnapshotStatuses = []domain.SessionStatus{
	domain.SessionStatusValid,
	domain.SessionStatusInvalid,
	domain.SessionStatusUnavailable,
}

var allowedTaskStatuses = setOf(
	string(domain.TaskStatusQueued),
	string(domain.TaskStatusRunning),
	string(domain.TaskStatusSuccess),
	string(domain.TaskStatusRetry),
	string(domain.TaskStatusFail),
	"unknown",
)

var allowedStages = setOf(
	"claim",
	"complete",
	"follow.warmup",
	"follow.execution",
	"follow.verify",
	"follow.finalize",
	"release",
	"refresh",
	"unknown",
)

var allowedExecutionOutcomes = setOf(
	string(domain.FollowFlowOutcomeCompleted),
	string(domain.FollowFlowOutcomeAlreadyDone),
	string(domain.FollowFlowOutcomeActionUnavailable),
	string(domain.FollowFlowOutcomeTargetUnreachable),
	string(domain.FollowFlowOutcomeNavigationFailed),
	"unknown",
)

var allowedDependencies = setOf(
	"postgres",
	"minio",
	"playwright",
	"unknown",
)

var allowedAccountStates = setOf(
	string(domain.AccountStateActive),
	string(domain.AccountStateBusy),
	string(domain.AccountStateInvalidSession),
	string(domain.AccountStateQuarantined),
	string(domain.AccountStateRestricted),
	"unknown",
)

var allowedSessionStatuses = setOf(
	string(domain.SessionStatusValid),
	string(domain.SessionStatusInvalid),
	string(domain.SessionStatusUnavailable),
	"unknown",
)

var allowedErrorCodes = setOf(
	string(domain.ErrorCodeEligible),
	string(domain.ErrorCodeInternal),
	string(domain.ErrorCodeAccountNotFound),
	string(domain.ErrorCodeAccountBusy),
	string(domain.ErrorCodeAccountInactive),
	string(domain.ErrorCodeAccountNotReady),
	string(domain.ErrorCodeAccountQuarantined),
	string(domain.ErrorCodeAccountRestricted),
	string(domain.ErrorCodeAccountMissingProxy),
	string(domain.ErrorCodeAccountProxyInactive),
	string(domain.ErrorCodeAccountLimitReached),
	string(domain.ErrorCodeAccountContextMismatch),
	string(domain.ErrorCodeInvalidOperationalState),
	string(domain.ErrorCodeInvalidExecutionContext),
	string(domain.ErrorCodeInvalidAccountIdentifier),
	string(domain.ErrorCodeSessionMetadataNotFound),
	string(domain.ErrorCodeSessionPayloadMissing),
	string(domain.ErrorCodeSessionPayloadCorrupted),
	string(domain.ErrorCodeSessionOwnershipMismatch),
	string(domain.ErrorCodeSessionPayloadInvalid),
	string(domain.ErrorCodeInvalidSessionStatus),
	string(domain.ErrorCodeInvalidSessionRevision),
	string(domain.ErrorCodeInvalidSessionObjectKey),
	string(domain.ErrorCodeTaskNotFound),
	string(domain.ErrorCodeTaskNotRunning),
	string(domain.ErrorCodeTaskClaimOwnerMismatch),
	string(domain.ErrorCodeInvalidTaskIdentifier),
	string(domain.ErrorCodeInvalidTaskStatus),
	string(domain.ErrorCodeInvalidTaskTransition),
	string(domain.ErrorCodeInvalidTaskClaimedBy),
	string(domain.ErrorCodeTaskCompletionReason),
	string(domain.ErrorCodeFollowTargetProfile),
	string(domain.ErrorCodeFollowActionUnavailable),
	string(domain.ErrorCodeFollowTargetUnreachable),
	string(domain.ErrorCodeFollowNavigationFailed),
	string(domain.ErrorCodeFollowVerifyFailed),
	string(domain.ErrorCodeArtifactPersistFailed),
	string(domain.ErrorCodeFollowResultPersistFailed),
	string(domain.ErrorCodeFollowResultNotFound),
	string(domain.ErrorCodeSessionSaveFailed),
	"unknown",
)

func normalizeTaskStatusLabel(status string) string {
	return normalizeBoundedLabel(status, allowedTaskStatuses, "unknown")
}

func normalizeStageLabel(stage string) string {
	return normalizeBoundedLabel(stage, allowedStages, "unknown")
}

func normalizeExecutionOutcomeLabel(outcome string) string {
	return normalizeBoundedLabel(outcome, allowedExecutionOutcomes, "unknown")
}

func normalizeDependencyLabel(dependency string) string {
	return normalizeBoundedLabel(dependency, allowedDependencies, "unknown")
}

func normalizeErrorCodeLabel(code string) string {
	return normalizeBoundedLabel(code, allowedErrorCodes, string(domain.ErrorCodeInternal))
}

func normalizeBoundedLabel(
	value string,
	allowed map[string]struct{},
	fallback string,
) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	if _, ok := allowed[normalized]; ok {
		return normalized
	}
	return fallback
}

func setOf(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[strings.TrimSpace(strings.ToLower(value))] = struct{}{}
	}
	return set
}
