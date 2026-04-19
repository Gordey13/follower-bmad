package httptransport

import (
	"time"

	"follower/internal/domain"
)

type adminTaskListResponseDTO struct {
	Tasks []adminTaskListDTO `json:"tasks"`
}

type adminTaskListDTO struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	TargetProfile string    `json:"target_profile"`
	Status        string    `json:"status"`
	Attempt       int       `json:"attempt"`
	ErrorCode     *string   `json:"error_code"`
	ResultReason  *string   `json:"result_reason"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func newAdminTaskListDTO(task domain.Task) adminTaskListDTO {
	return adminTaskListDTO{
		ID:            task.ID.String(),
		AccountID:     task.AccountID.String(),
		TargetProfile: string(task.TargetProfile),
		Status:        string(task.Status),
		Attempt:       task.Attempt,
		ErrorCode:     adminNullableString(string(task.ErrorCode)),
		ResultReason:  adminNullableString(task.ResultReason),
		UpdatedAt:     task.UpdatedAt,
	}
}
