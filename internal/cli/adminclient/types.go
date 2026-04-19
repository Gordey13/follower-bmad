package adminclient

import "time"

type TaskListResponse struct {
	Tasks []TaskListItem `json:"tasks"`
}

type TaskListItem struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	TargetProfile string    `json:"target_profile"`
	Status        string    `json:"status"`
	Attempt       int       `json:"attempt"`
	ErrorCode     *string   `json:"error_code"`
	ResultReason  *string   `json:"result_reason"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type TaskDetail struct {
	ID             string              `json:"id"`
	AccountID      string              `json:"account_id"`
	TargetProfile  string              `json:"target_profile"`
	Status         string              `json:"status"`
	Attempt        int                 `json:"attempt"`
	ClaimedBy      *string             `json:"claimed_by"`
	ClaimedAt      *time.Time          `json:"claimed_at"`
	StartedAt      *time.Time          `json:"started_at"`
	FinishedAt     *time.Time          `json:"finished_at"`
	ErrorCode      *string             `json:"error_code"`
	ResultReason   *string             `json:"result_reason"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	AttemptContext *TaskAttemptContext `json:"attempt_context"`
}

type TaskAttemptContext struct {
	Outcome             string   `json:"outcome"`
	Verified            bool     `json:"verified"`
	VerificationSignal  string   `json:"verification_signal"`
	VerificationReason  *string  `json:"verification_reason"`
	ErrorCode           *string  `json:"error_code"`
	ScreenshotObjectKey string   `json:"screenshot_object_key"`
	ArtifactObjectKeys  []string `json:"artifact_object_keys"`
}

type TaskFailuresResponse struct {
	Tasks []TaskFailureItem `json:"tasks"`
}

type TaskFailureItem struct {
	ID                 string    `json:"id"`
	AccountID          string    `json:"account_id"`
	TargetProfile      string    `json:"target_profile"`
	Status             string    `json:"status"`
	Attempt            int       `json:"attempt"`
	ErrorCode          *string   `json:"error_code"`
	ResultReason       *string   `json:"result_reason"`
	UpdatedAt          time.Time `json:"updated_at"`
	FollowOutcome      *string   `json:"follow_outcome"`
	VerificationSignal *string   `json:"verification_signal"`
}

type TaskRetryResponse struct {
	SourceTaskID  string `json:"source_task_id"`
	NewTaskID     string `json:"new_task_id"`
	Status        string `json:"status"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type TaskCancelResponse struct {
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	ResultReason  string `json:"result_reason"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type ErrorKind string

const (
	ErrorKindValidation ErrorKind = "validation_error"
	ErrorKindNetwork    ErrorKind = "network_error"
	ErrorKindProtocol   ErrorKind = "protocol_error"
	ErrorKindAPI        ErrorKind = "api_error"
)

type Error struct {
	Kind          ErrorKind
	Code          string
	Message       string
	StatusCode    int
	CorrelationID string
	Cause         error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "admin API request failed"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
