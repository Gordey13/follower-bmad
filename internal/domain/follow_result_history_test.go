package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFollowResultsHistoryQueryValidate(t *testing.T) {
	t.Parallel()

	valid := FollowResultsHistoryQuery{
		AccountID:     uuid.New(),
		TargetProfile: "target-history-01",
		Outcome:       FollowFlowOutcomeCompleted,
		TaskStatus:    TaskStatusSuccess,
		Limit:         25,
		Offset:        0,
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid query, got error: %v", err)
	}
}

func TestFollowResultsHistoryQueryValidateRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query FollowResultsHistoryQuery
		code  ErrorCode
	}{
		{
			name: "invalid target profile",
			query: FollowResultsHistoryQuery{
				TargetProfile: " ",
				Limit:         10,
			},
			code: ErrorCodeFollowTargetProfile,
		},
		{
			name: "invalid outcome",
			query: FollowResultsHistoryQuery{
				Outcome: FollowFlowOutcome("unknown"),
				Limit:   10,
			},
			code: ErrorCodeFollowResultPersistFailed,
		},
		{
			name: "invalid task status",
			query: FollowResultsHistoryQuery{
				TaskStatus: TaskStatus("unknown"),
				Limit:      10,
			},
			code: ErrorCodeInvalidTaskStatus,
		},
		{
			name: "invalid limit",
			query: FollowResultsHistoryQuery{
				Limit: 0,
			},
			code: ErrorCodeInvalidTaskTransition,
		},
		{
			name: "invalid offset",
			query: FollowResultsHistoryQuery{
				Limit:  10,
				Offset: -1,
			},
			code: ErrorCodeInvalidTaskTransition,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.query.Validate()
			if err == nil {
				t.Fatalf("expected error %s, got nil", tc.code)
			}
			if !IsDomainErrorCode(err, tc.code) {
				t.Fatalf("expected error code %s, got %v", tc.code, err)
			}
		})
	}
}

func TestFollowResultsHistoryQueryValidateRejectsInvertedTimeRange(t *testing.T) {
	t.Parallel()

	from := time.Now().UTC()
	to := from.Add(-time.Minute)

	query := FollowResultsHistoryQuery{
		From:  &from,
		To:    &to,
		Limit: 10,
	}

	err := query.Validate()
	if err == nil {
		t.Fatal("expected error for inverted time range")
	}
	if !IsDomainErrorCode(err, ErrorCodeInvalidTaskTransition) {
		t.Fatalf("expected error code %s, got %v", ErrorCodeInvalidTaskTransition, err)
	}
}
