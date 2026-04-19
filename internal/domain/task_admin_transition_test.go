package domain

import "testing"

func TestEvaluateTaskAdminTransitionRetryAllowedStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fromStatus TaskStatus
	}{
		{name: "from retry", fromStatus: TaskStatusRetry},
		{name: "from fail", fromStatus: TaskStatusFail},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			transition, err := EvaluateTaskAdminTransition(tc.fromStatus, TaskAdminActionRetry)
			if err != nil {
				t.Fatalf("EvaluateTaskAdminTransition() error = %v", err)
			}
			if transition.Action != TaskAdminActionRetry {
				t.Fatalf("expected action %q, got %q", TaskAdminActionRetry, transition.Action)
			}
			if transition.FromStatus != tc.fromStatus {
				t.Fatalf("expected from_status %q, got %q", tc.fromStatus, transition.FromStatus)
			}
			if transition.ToStatus != TaskStatusQueued {
				t.Fatalf("expected to_status %q, got %q", TaskStatusQueued, transition.ToStatus)
			}
		})
	}
}

func TestEvaluateTaskAdminTransitionCancelAllowedStatuses(t *testing.T) {
	t.Parallel()

	transition, err := EvaluateTaskAdminTransition(TaskStatusQueued, TaskAdminActionCancel)
	if err != nil {
		t.Fatalf("EvaluateTaskAdminTransition() error = %v", err)
	}
	if transition.Action != TaskAdminActionCancel {
		t.Fatalf("expected action %q, got %q", TaskAdminActionCancel, transition.Action)
	}
	if transition.FromStatus != TaskStatusQueued {
		t.Fatalf("expected from_status %q, got %q", TaskStatusQueued, transition.FromStatus)
	}
	if transition.ToStatus != TaskStatusCanceled {
		t.Fatalf("expected to_status %q, got %q", TaskStatusCanceled, transition.ToStatus)
	}
}

func TestEvaluateTaskAdminTransitionDenyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		fromStatus    TaskStatus
		action        TaskAdminAction
		wantErrorCode ErrorCode
	}{
		{
			name:          "retry from queued denied",
			fromStatus:    TaskStatusQueued,
			action:        TaskAdminActionRetry,
			wantErrorCode: ErrorCodeRetryNotAllowed,
		},
		{
			name:          "retry from running denied",
			fromStatus:    TaskStatusRunning,
			action:        TaskAdminActionRetry,
			wantErrorCode: ErrorCodeRetryNotAllowed,
		},
		{
			name:          "retry from canceled denied",
			fromStatus:    TaskStatusCanceled,
			action:        TaskAdminActionRetry,
			wantErrorCode: ErrorCodeRetryNotAllowed,
		},
		{
			name:          "cancel from running denied",
			fromStatus:    TaskStatusRunning,
			action:        TaskAdminActionCancel,
			wantErrorCode: ErrorCodeCancelNotAllowed,
		},
		{
			name:          "cancel from success denied",
			fromStatus:    TaskStatusSuccess,
			action:        TaskAdminActionCancel,
			wantErrorCode: ErrorCodeCancelNotAllowed,
		},
		{
			name:          "cancel from retry denied",
			fromStatus:    TaskStatusRetry,
			action:        TaskAdminActionCancel,
			wantErrorCode: ErrorCodeCancelNotAllowed,
		},
		{
			name:          "cancel from fail denied",
			fromStatus:    TaskStatusFail,
			action:        TaskAdminActionCancel,
			wantErrorCode: ErrorCodeCancelNotAllowed,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := EvaluateTaskAdminTransition(tc.fromStatus, tc.action)
			if !IsDomainErrorCode(err, tc.wantErrorCode) {
				t.Fatalf("expected %s, got %v", tc.wantErrorCode, err)
			}
		})
	}
}

func TestEvaluateTaskAdminTransitionRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if _, err := EvaluateTaskAdminTransition(TaskStatus("unknown"), TaskAdminActionRetry); !IsDomainErrorCode(err, ErrorCodeTaskStateConflict) {
		t.Fatalf("expected %s for invalid status, got %v", ErrorCodeTaskStateConflict, err)
	}

	if _, err := EvaluateTaskAdminTransition(TaskStatusQueued, TaskAdminAction("reopen")); !IsDomainErrorCode(err, ErrorCodeTaskStateConflict) {
		t.Fatalf("expected %s for invalid action, got %v", ErrorCodeTaskStateConflict, err)
	}
}

func TestMapTaskAdminTransitionErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want TaskAdminTransitionErrorCode
	}{
		{
			name: "retry not allowed",
			err:  NewDomainError(ErrorCodeRetryNotAllowed, "retry denied"),
			want: TaskAdminTransitionErrorCodeRetryNotAllowed,
		},
		{
			name: "cancel not allowed",
			err:  NewDomainError(ErrorCodeCancelNotAllowed, "cancel denied"),
			want: TaskAdminTransitionErrorCodeCancelNotAllowed,
		},
		{
			name: "fallback task state conflict",
			err:  NewDomainError(ErrorCodeTaskStateConflict, "conflict"),
			want: TaskAdminTransitionErrorCodeTaskStateConflict,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := MapTaskAdminTransitionErrorCode(tc.err); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
