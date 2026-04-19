package httptransport

import (
	"strings"
	"time"

	"follower/internal/domain"

	"github.com/google/uuid"
)

type adminTaskDetailDTO struct {
	ID             string                      `json:"id"`
	SourceTaskID   *string                     `json:"source_task_id"`
	AccountID      string                      `json:"account_id"`
	TargetProfile  string                      `json:"target_profile"`
	Status         string                      `json:"status"`
	Attempt        int                         `json:"attempt"`
	ClaimedBy      *string                     `json:"claimed_by"`
	ClaimedAt      *time.Time                  `json:"claimed_at"`
	StartedAt      *time.Time                  `json:"started_at"`
	FinishedAt     *time.Time                  `json:"finished_at"`
	ErrorCode      *string                     `json:"error_code"`
	ResultReason   *string                     `json:"result_reason"`
	CreatedAt      time.Time                   `json:"created_at"`
	UpdatedAt      time.Time                   `json:"updated_at"`
	AttemptContext *adminTaskAttemptContextDTO `json:"attempt_context"`
}

type adminTaskAttemptContextDTO struct {
	Outcome             string   `json:"outcome"`
	Verified            bool     `json:"verified"`
	VerificationSignal  string   `json:"verification_signal"`
	VerificationReason  *string  `json:"verification_reason"`
	ErrorCode           *string  `json:"error_code"`
	ScreenshotObjectKey string   `json:"screenshot_object_key"`
	ArtifactObjectKeys  []string `json:"artifact_object_keys"`
}

func newAdminTaskDetailDTO(task domain.Task) adminTaskDetailDTO {
	return adminTaskDetailDTO{
		ID:             task.ID.String(),
		SourceTaskID:   adminNullableUUID(task.SourceTaskID),
		AccountID:      task.AccountID.String(),
		TargetProfile:  string(task.TargetProfile),
		Status:         string(task.Status),
		Attempt:        task.Attempt,
		ClaimedBy:      adminNullableString(task.ClaimedBy),
		ClaimedAt:      task.ClaimedAt,
		StartedAt:      task.StartedAt,
		FinishedAt:     task.FinishedAt,
		ErrorCode:      adminNullableString(string(task.ErrorCode)),
		ResultReason:   adminNullableString(task.ResultReason),
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
		AttemptContext: nil,
	}
}

func adminNullableUUID(value *uuid.UUID) *string {
	if value == nil || *value == uuid.Nil {
		return nil
	}
	normalized := value.String()
	return &normalized
}

func newAdminTaskAttemptContextDTO(result domain.FollowResult) *adminTaskAttemptContextDTO {
	return &adminTaskAttemptContextDTO{
		Outcome:             string(result.Outcome),
		Verified:            result.Verified,
		VerificationSignal:  string(result.VerificationSignal),
		VerificationReason:  adminNullableString(result.VerificationReason),
		ErrorCode:           adminNullableString(string(result.ErrorCode)),
		ScreenshotObjectKey: result.ScreenshotObjectKey,
		ArtifactObjectKeys:  append([]string(nil), result.ArtifactObjectKeys...),
	}
}

func adminNullableString(value string) *string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil
	}
	return &normalized
}
