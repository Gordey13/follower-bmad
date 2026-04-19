package httptransport

import (
	"time"

	"follower/internal/domain"
)

type adminTaskFailuresResponseDTO struct {
	Tasks []adminTaskFailureDTO `json:"tasks"`
}

type adminTaskFailureDTO struct {
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

func newAdminTaskFailureDTO(task domain.Task) adminTaskFailureDTO {
	return adminTaskFailureDTO{
		ID:                 task.ID.String(),
		AccountID:          task.AccountID.String(),
		TargetProfile:      string(task.TargetProfile),
		Status:             string(task.Status),
		Attempt:            task.Attempt,
		ErrorCode:          adminNullableString(string(task.ErrorCode)),
		ResultReason:       adminNullableString(task.ResultReason),
		UpdatedAt:          task.UpdatedAt,
		FollowOutcome:      nil,
		VerificationSignal: nil,
	}
}

func enrichAdminTaskFailureDTO(
	dto *adminTaskFailureDTO,
	result domain.FollowResult,
) {
	if dto == nil {
		return
	}
	dto.FollowOutcome = adminNullableString(string(result.Outcome))
	dto.VerificationSignal = adminNullableString(string(result.VerificationSignal))
}
