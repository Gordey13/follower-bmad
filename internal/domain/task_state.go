package domain

type TaskStatus string

const (
	TaskStatusQueued   TaskStatus = "queued"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusSuccess  TaskStatus = "success"
	TaskStatusRetry    TaskStatus = "retry"
	TaskStatusFail     TaskStatus = "fail"
	TaskStatusCanceled TaskStatus = "canceled"
)

func (status TaskStatus) IsValid() bool {
	switch status {
	case TaskStatusQueued,
		TaskStatusRunning,
		TaskStatusSuccess,
		TaskStatusRetry,
		TaskStatusFail,
		TaskStatusCanceled:
		return true
	default:
		return false
	}
}

func (status TaskStatus) IsTerminal() bool {
	switch status {
	case TaskStatusSuccess, TaskStatusRetry, TaskStatusFail, TaskStatusCanceled:
		return true
	default:
		return false
	}
}

func (status TaskStatus) CanTransitionTo(next TaskStatus) bool {
	switch status {
	case TaskStatusQueued:
		return next == TaskStatusRunning
	case TaskStatusRunning:
		return next == TaskStatusSuccess || next == TaskStatusRetry || next == TaskStatusFail
	default:
		return false
	}
}
