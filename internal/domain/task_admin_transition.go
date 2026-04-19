package domain

import "fmt"

type TaskAdminAction string

const (
	TaskAdminActionRetry  TaskAdminAction = "retry"
	TaskAdminActionCancel TaskAdminAction = "cancel"
)

type TaskAdminTransition struct {
	Action     TaskAdminAction
	FromStatus TaskStatus
	ToStatus   TaskStatus
}

type TaskAdminTransitionErrorCode string

const (
	TaskAdminTransitionErrorCodeTaskStateConflict TaskAdminTransitionErrorCode = "TASK_STATE_CONFLICT"
	TaskAdminTransitionErrorCodeRetryNotAllowed   TaskAdminTransitionErrorCode = "RETRY_NOT_ALLOWED"
	TaskAdminTransitionErrorCodeCancelNotAllowed  TaskAdminTransitionErrorCode = "CANCEL_NOT_ALLOWED"
)

var taskAdminTransitionMatrix = map[TaskAdminAction]map[TaskStatus]TaskStatus{
	TaskAdminActionRetry: {
		TaskStatusRetry: TaskStatusQueued,
		TaskStatusFail:  TaskStatusQueued,
	},
	TaskAdminActionCancel: {
		TaskStatusQueued: TaskStatusCanceled,
	},
}

func EvaluateTaskAdminTransition(fromStatus TaskStatus, action TaskAdminAction) (TaskAdminTransition, error) {
	if !fromStatus.IsValid() {
		return TaskAdminTransition{}, NewDomainError(
			ErrorCodeTaskStateConflict,
			fmt.Sprintf("admin transition rejected: invalid source status %q", fromStatus),
		)
	}

	actionTransitions, ok := taskAdminTransitionMatrix[action]
	if !ok {
		return TaskAdminTransition{}, NewDomainError(
			ErrorCodeTaskStateConflict,
			fmt.Sprintf("admin transition rejected: unsupported action %q", action),
		)
	}

	toStatus, allowed := actionTransitions[fromStatus]
	if !allowed {
		return TaskAdminTransition{}, NewDomainError(
			taskAdminTransitionDeniedCode(action),
			fmt.Sprintf(
				"admin transition denied: action=%s source_status=%s",
				action,
				fromStatus,
			),
		)
	}

	return TaskAdminTransition{
		Action:     action,
		FromStatus: fromStatus,
		ToStatus:   toStatus,
	}, nil
}

func MapTaskAdminTransitionErrorCode(err error) TaskAdminTransitionErrorCode {
	switch {
	case IsDomainErrorCode(err, ErrorCodeRetryNotAllowed):
		return TaskAdminTransitionErrorCodeRetryNotAllowed
	case IsDomainErrorCode(err, ErrorCodeCancelNotAllowed):
		return TaskAdminTransitionErrorCodeCancelNotAllowed
	default:
		return TaskAdminTransitionErrorCodeTaskStateConflict
	}
}

func taskAdminTransitionDeniedCode(action TaskAdminAction) ErrorCode {
	switch action {
	case TaskAdminActionRetry:
		return ErrorCodeRetryNotAllowed
	case TaskAdminActionCancel:
		return ErrorCodeCancelNotAllowed
	default:
		return ErrorCodeTaskStateConflict
	}
}
