package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"follower/internal/cli/adminclient"
	"follower/internal/cli/render"

	"github.com/google/uuid"
)

type Dependencies struct {
	Client *adminclient.Client
	Stdout io.Writer
	Stderr io.Writer
}

func Execute(ctx context.Context, args []string, deps Dependencies) int {
	stdout, stderr := normalizeWriters(deps.Stdout, deps.Stderr)
	if deps.Client == nil {
		writePlainError(stderr, "CLI_CONFIG_ERROR", "admin client is not configured")
		return 1
	}

	if len(args) == 0 {
		writeUsageError(stderr, "CLI_USAGE_ERROR", "command is required")
		return 1
	}
	if args[0] != "tasks" {
		writeUsageError(stderr, "CLI_USAGE_ERROR", "only tasks command group is supported")
		return 1
	}
	if len(args) < 2 {
		writeUsageError(stderr, "CLI_USAGE_ERROR", "tasks subcommand is required")
		return 1
	}

	switch args[1] {
	case "list":
		return runTasksList(ctx, args[2:], deps.Client, stdout, stderr)
	case "get":
		return runTasksGet(ctx, args[2:], deps.Client, stdout, stderr)
	case "retry":
		return runTasksRetry(ctx, args[2:], deps.Client, stdout, stderr)
	case "cancel":
		return runTasksCancel(ctx, args[2:], deps.Client, stdout, stderr)
	case "failures":
		return runTasksFailures(ctx, args[2:], deps.Client, stdout, stderr)
	default:
		writeUsageError(stderr, "CLI_USAGE_ERROR", "unsupported tasks subcommand")
		return 1
	}
}

func runTasksList(
	ctx context.Context,
	args []string,
	client *adminclient.Client,
	stdout io.Writer,
	stderr io.Writer,
) int {
	mode, remaining, err := parseOutputFlag(args)
	if err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", err.Error(), "")
		return 1
	}
	if len(remaining) != 0 {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", "tasks list does not accept positional arguments", "")
		return 1
	}

	response, err := client.ListTasks(ctx)
	if err != nil {
		writeNormalizedError(mode, stdout, stderr, err)
		return 1
	}

	view := mapTaskListView(response)
	if err := renderTasksList(mode, stdout, view); err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_RENDER_ERROR", "failed to render tasks list output", "")
		return 1
	}

	return 0
}

func runTasksGet(
	ctx context.Context,
	args []string,
	client *adminclient.Client,
	stdout io.Writer,
	stderr io.Writer,
) int {
	mode, remaining, err := parseOutputFlag(args)
	if err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", err.Error(), "")
		return 1
	}
	if len(remaining) != 1 {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", "tasks get requires exactly one task id", "")
		return 1
	}

	taskID := strings.TrimSpace(remaining[0])
	if _, err := uuid.Parse(taskID); err != nil {
		writeCommandError(mode, stdout, stderr, "TASK_ID_INVALID", "task id must be a valid uuid", "")
		return 1
	}

	response, err := client.GetTask(ctx, taskID)
	if err != nil {
		writeNormalizedError(mode, stdout, stderr, err)
		return 1
	}

	view := mapTaskDetailView(response)
	if err := renderTaskDetail(mode, stdout, view); err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_RENDER_ERROR", "failed to render task detail output", "")
		return 1
	}

	return 0
}

func runTasksFailures(
	ctx context.Context,
	args []string,
	client *adminclient.Client,
	stdout io.Writer,
	stderr io.Writer,
) int {
	mode, remaining, err := parseOutputFlag(args)
	if err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", err.Error(), "")
		return 1
	}
	if len(remaining) != 0 {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", "tasks failures does not accept positional arguments", "")
		return 1
	}

	response, err := client.ListFailures(ctx)
	if err != nil {
		writeNormalizedError(mode, stdout, stderr, err)
		return 1
	}

	view := mapTaskFailuresView(response)
	if err := renderTaskFailures(mode, stdout, view); err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_RENDER_ERROR", "failed to render task failures output", "")
		return 1
	}

	return 0
}

func runTasksRetry(
	ctx context.Context,
	args []string,
	client *adminclient.Client,
	stdout io.Writer,
	stderr io.Writer,
) int {
	mode, remaining, err := parseOutputFlag(args)
	if err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", err.Error(), "")
		return 1
	}
	if len(remaining) != 1 {
		writeCommandError(
			mode,
			stdout,
			stderr,
			"CLI_USAGE_ERROR",
			"tasks retry requires exactly one task id",
			"",
		)
		return 1
	}

	taskID := strings.TrimSpace(remaining[0])
	if _, err := uuid.Parse(taskID); err != nil {
		writeCommandError(mode, stdout, stderr, "TASK_ID_INVALID", "task id must be a valid uuid", "")
		return 1
	}

	response, err := client.RetryTask(ctx, taskID)
	if err != nil {
		writeNormalizedError(mode, stdout, stderr, err)
		return 1
	}

	view := taskActionView{
		Action:        "retry requested",
		TaskID:        response.SourceTaskID,
		NewTaskID:     response.NewTaskID,
		CorrelationID: response.CorrelationID,
	}
	if err := renderTaskAction(mode, stdout, view); err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_RENDER_ERROR", "failed to render retry output", "")
		return 1
	}

	return 0
}

func runTasksCancel(
	ctx context.Context,
	args []string,
	client *adminclient.Client,
	stdout io.Writer,
	stderr io.Writer,
) int {
	mode, remaining, err := parseOutputFlag(args)
	if err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_USAGE_ERROR", err.Error(), "")
		return 1
	}
	if len(remaining) != 1 {
		writeCommandError(
			mode,
			stdout,
			stderr,
			"CLI_USAGE_ERROR",
			"tasks cancel requires exactly one task id",
			"",
		)
		return 1
	}

	taskID := strings.TrimSpace(remaining[0])
	if _, err := uuid.Parse(taskID); err != nil {
		writeCommandError(mode, stdout, stderr, "TASK_ID_INVALID", "task id must be a valid uuid", "")
		return 1
	}

	response, err := client.CancelTask(ctx, taskID)
	if err != nil {
		writeNormalizedError(mode, stdout, stderr, err)
		return 1
	}

	view := taskActionView{
		Action:        "cancel requested",
		TaskID:        response.TaskID,
		CorrelationID: response.CorrelationID,
	}
	if err := renderTaskAction(mode, stdout, view); err != nil {
		writeCommandError(mode, stdout, stderr, "CLI_RENDER_ERROR", "failed to render cancel output", "")
		return 1
	}

	return 0
}

func parseOutputFlag(args []string) (render.OutputMode, []string, error) {
	modeRaw := string(render.OutputModeTable)
	remaining := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		current := strings.TrimSpace(args[i])
		switch {
		case current == "--output":
			if i+1 >= len(args) {
				return render.OutputModeTable, nil, errors.New("--output flag requires a value")
			}
			modeRaw = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(current, "--output="):
			modeRaw = strings.TrimSpace(strings.TrimPrefix(current, "--output="))
		default:
			remaining = append(remaining, args[i])
		}
	}

	mode, err := render.ParseOutputMode(modeRaw)
	if err != nil {
		return render.OutputModeTable, nil, err
	}

	return mode, remaining, nil
}

type tasksListView struct {
	Tasks []tasksListItem `json:"tasks"`
}

type tasksListItem struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	TargetProfile string     `json:"target_profile"`
	Status        string     `json:"status"`
	Attempt       int        `json:"attempt"`
	ErrorCode     *string    `json:"error_code"`
	ResultReason  *string    `json:"result_reason"`
	UpdatedAt     *time.Time `json:"updated_at"`
}

type taskDetailView struct {
	ID             string                  `json:"id"`
	AccountID      string                  `json:"account_id"`
	TargetProfile  string                  `json:"target_profile"`
	Status         string                  `json:"status"`
	Attempt        int                     `json:"attempt"`
	ClaimedBy      *string                 `json:"claimed_by"`
	ClaimedAt      *time.Time              `json:"claimed_at"`
	StartedAt      *time.Time              `json:"started_at"`
	FinishedAt     *time.Time              `json:"finished_at"`
	ErrorCode      *string                 `json:"error_code"`
	ResultReason   *string                 `json:"result_reason"`
	CreatedAt      *time.Time              `json:"created_at"`
	UpdatedAt      *time.Time              `json:"updated_at"`
	AttemptContext *taskAttemptContextView `json:"attempt_context"`
}

type taskAttemptContextView struct {
	Outcome             string   `json:"outcome"`
	Verified            bool     `json:"verified"`
	VerificationSignal  string   `json:"verification_signal"`
	VerificationReason  *string  `json:"verification_reason"`
	ErrorCode           *string  `json:"error_code"`
	ScreenshotObjectKey string   `json:"screenshot_object_key"`
	ArtifactObjectKeys  []string `json:"artifact_object_keys"`
}

type taskFailuresView struct {
	Tasks []taskFailureItemView `json:"tasks"`
}

type taskFailureItemView struct {
	ID                 string     `json:"id"`
	AccountID          string     `json:"account_id"`
	TargetProfile      string     `json:"target_profile"`
	Status             string     `json:"status"`
	Attempt            int        `json:"attempt"`
	ErrorCode          *string    `json:"error_code"`
	ResultReason       *string    `json:"result_reason"`
	FollowOutcome      *string    `json:"follow_outcome"`
	VerificationSignal *string    `json:"verification_signal"`
	UpdatedAt          *time.Time `json:"updated_at"`
}

type taskActionView struct {
	Action        string `json:"action"`
	TaskID        string `json:"task_id"`
	NewTaskID     string `json:"new_task_id,omitempty"`
	CorrelationID string `json:"-"`
}

func mapTaskListView(response adminclient.TaskListResponse) tasksListView {
	items := make([]tasksListItem, 0, len(response.Tasks))
	for _, item := range response.Tasks {
		updatedAt := item.UpdatedAt
		items = append(items, tasksListItem{
			ID:            item.ID,
			AccountID:     item.AccountID,
			TargetProfile: item.TargetProfile,
			Status:        item.Status,
			Attempt:       item.Attempt,
			ErrorCode:     item.ErrorCode,
			ResultReason:  item.ResultReason,
			UpdatedAt:     nullableTime(updatedAt),
		})
	}
	return tasksListView{Tasks: items}
}

func mapTaskDetailView(response adminclient.TaskDetail) taskDetailView {
	createdAt := response.CreatedAt
	updatedAt := response.UpdatedAt
	var contextView *taskAttemptContextView
	if response.AttemptContext != nil {
		contextView = &taskAttemptContextView{
			Outcome:             response.AttemptContext.Outcome,
			Verified:            response.AttemptContext.Verified,
			VerificationSignal:  response.AttemptContext.VerificationSignal,
			VerificationReason:  response.AttemptContext.VerificationReason,
			ErrorCode:           response.AttemptContext.ErrorCode,
			ScreenshotObjectKey: response.AttemptContext.ScreenshotObjectKey,
			ArtifactObjectKeys:  append([]string(nil), response.AttemptContext.ArtifactObjectKeys...),
		}
	}
	return taskDetailView{
		ID:             response.ID,
		AccountID:      response.AccountID,
		TargetProfile:  response.TargetProfile,
		Status:         response.Status,
		Attempt:        response.Attempt,
		ClaimedBy:      response.ClaimedBy,
		ClaimedAt:      response.ClaimedAt,
		StartedAt:      response.StartedAt,
		FinishedAt:     response.FinishedAt,
		ErrorCode:      response.ErrorCode,
		ResultReason:   response.ResultReason,
		CreatedAt:      nullableTime(createdAt),
		UpdatedAt:      nullableTime(updatedAt),
		AttemptContext: contextView,
	}
}

func mapTaskFailuresView(response adminclient.TaskFailuresResponse) taskFailuresView {
	items := make([]taskFailureItemView, 0, len(response.Tasks))
	for _, item := range response.Tasks {
		updatedAt := item.UpdatedAt
		items = append(items, taskFailureItemView{
			ID:                 item.ID,
			AccountID:          item.AccountID,
			TargetProfile:      item.TargetProfile,
			Status:             item.Status,
			Attempt:            item.Attempt,
			ErrorCode:          item.ErrorCode,
			ResultReason:       item.ResultReason,
			FollowOutcome:      item.FollowOutcome,
			VerificationSignal: item.VerificationSignal,
			UpdatedAt:          nullableTime(updatedAt),
		})
	}
	return taskFailuresView{Tasks: items}
}

func renderTasksList(mode render.OutputMode, stdout io.Writer, view tasksListView) error {
	if mode == render.OutputModeJSON {
		return render.WriteJSON(stdout, view)
	}

	rows := make([][]string, 0, len(view.Tasks))
	for _, item := range view.Tasks {
		rows = append(rows, []string{
			item.ID,
			item.AccountID,
			item.TargetProfile,
			item.Status,
			fmt.Sprintf("%d", item.Attempt),
			derefString(item.ErrorCode),
			derefString(item.ResultReason),
			formatOptionalTime(item.UpdatedAt),
		})
	}

	return render.WriteTable(stdout, render.Table{
		Headers: []string{
			"ID",
			"ACCOUNT_ID",
			"TARGET_PROFILE",
			"STATUS",
			"ATTEMPT",
			"ERROR_CODE",
			"RESULT_REASON",
			"UPDATED_AT",
		},
		Rows: rows,
	})
}

func renderTaskDetail(mode render.OutputMode, stdout io.Writer, view taskDetailView) error {
	if mode == render.OutputModeJSON {
		return render.WriteJSON(stdout, view)
	}

	verified := ""
	outcome := ""
	verificationSignal := ""
	verificationReason := ""
	attemptErrorCode := ""
	screenshotObjectKey := ""
	artifactObjectKeys := ""
	if view.AttemptContext != nil {
		verified = fmt.Sprintf("%t", view.AttemptContext.Verified)
		outcome = view.AttemptContext.Outcome
		verificationSignal = view.AttemptContext.VerificationSignal
		verificationReason = derefString(view.AttemptContext.VerificationReason)
		attemptErrorCode = derefString(view.AttemptContext.ErrorCode)
		screenshotObjectKey = view.AttemptContext.ScreenshotObjectKey
		artifactObjectKeys = strings.Join(view.AttemptContext.ArtifactObjectKeys, ",")
	}

	rows := [][]string{
		{"id", view.ID},
		{"account_id", view.AccountID},
		{"target_profile", view.TargetProfile},
		{"status", view.Status},
		{"attempt", fmt.Sprintf("%d", view.Attempt)},
		{"claimed_by", derefString(view.ClaimedBy)},
		{"claimed_at", formatOptionalTime(view.ClaimedAt)},
		{"started_at", formatOptionalTime(view.StartedAt)},
		{"finished_at", formatOptionalTime(view.FinishedAt)},
		{"error_code", derefString(view.ErrorCode)},
		{"result_reason", derefString(view.ResultReason)},
		{"created_at", formatOptionalTime(view.CreatedAt)},
		{"updated_at", formatOptionalTime(view.UpdatedAt)},
		{"attempt_context.outcome", outcome},
		{"attempt_context.verified", verified},
		{"attempt_context.verification_signal", verificationSignal},
		{"attempt_context.verification_reason", verificationReason},
		{"attempt_context.error_code", attemptErrorCode},
		{"attempt_context.screenshot_object_key", screenshotObjectKey},
		{"attempt_context.artifact_object_keys", artifactObjectKeys},
	}

	return render.WriteTable(stdout, render.Table{
		Headers: []string{"FIELD", "VALUE"},
		Rows:    rows,
	})
}

func renderTaskFailures(mode render.OutputMode, stdout io.Writer, view taskFailuresView) error {
	if mode == render.OutputModeJSON {
		return render.WriteJSON(stdout, view)
	}

	rows := make([][]string, 0, len(view.Tasks))
	for _, item := range view.Tasks {
		rows = append(rows, []string{
			item.ID,
			item.AccountID,
			item.TargetProfile,
			item.Status,
			fmt.Sprintf("%d", item.Attempt),
			derefString(item.ErrorCode),
			derefString(item.ResultReason),
			derefString(item.FollowOutcome),
			derefString(item.VerificationSignal),
			formatOptionalTime(item.UpdatedAt),
		})
	}

	return render.WriteTable(stdout, render.Table{
		Headers: []string{
			"ID",
			"ACCOUNT_ID",
			"TARGET_PROFILE",
			"STATUS",
			"ATTEMPT",
			"ERROR_CODE",
			"RESULT_REASON",
			"FOLLOW_OUTCOME",
			"VERIFICATION_SIGNAL",
			"UPDATED_AT",
		},
		Rows: rows,
	})
}

func renderTaskAction(mode render.OutputMode, stdout io.Writer, view taskActionView) error {
	if mode == render.OutputModeJSON {
		payload := map[string]any{
			"action":  view.Action,
			"task_id": view.TaskID,
		}
		if strings.TrimSpace(view.NewTaskID) != "" {
			payload["new_task_id"] = view.NewTaskID
		}
		if strings.TrimSpace(view.CorrelationID) != "" {
			payload["meta"] = map[string]string{
				"correlation_id": view.CorrelationID,
			}
		}
		return render.WriteJSON(stdout, payload)
	}

	return render.WriteActionResult(stdout, render.ActionResult{
		Action:    view.Action,
		TaskID:    view.TaskID,
		NewTaskID: view.NewTaskID,
	})
}

func normalizeWriters(stdout io.Writer, stderr io.Writer) (io.Writer, io.Writer) {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return stdout, stderr
}

func writeUsageError(stderr io.Writer, code string, message string) {
	writePlainError(stderr, code, message)
	_, _ = fmt.Fprintln(stderr, cliUsageText)
}

func writePlainError(stderr io.Writer, code string, message string) {
	_, _ = fmt.Fprintf(stderr, "error [%s]: %s\n", code, strings.TrimSpace(message))
}

func writeNormalizedError(mode render.OutputMode, stdout io.Writer, stderr io.Writer, err error) {
	info := normalizeError(err)
	writeCommandError(mode, stdout, stderr, info.Code, info.Message, info.CorrelationID)
}

func writeCommandError(
	mode render.OutputMode,
	stdout io.Writer,
	stderr io.Writer,
	code string,
	message string,
	correlationID string,
) {
	normalizedCode := strings.TrimSpace(code)
	if normalizedCode == "" {
		normalizedCode = "CLI_ERROR"
	}

	normalizedMessage := strings.TrimSpace(message)
	if normalizedMessage == "" {
		normalizedMessage = "command failed"
	}

	if mode == render.OutputModeJSON {
		_ = render.WriteErrorJSON(stderr, render.ErrorPayload{
			Code:          normalizedCode,
			Message:       normalizedMessage,
			CorrelationID: correlationID,
		})
		return
	}

	_ = stdout
	writePlainError(stderr, normalizedCode, normalizedMessage)
}

type commandErrorInfo struct {
	Code          string
	Message       string
	CorrelationID string
}

func normalizeError(err error) commandErrorInfo {
	var clientErr *adminclient.Error
	if errors.As(err, &clientErr) {
		code := strings.TrimSpace(clientErr.Code)
		if code == "" {
			switch clientErr.Kind {
			case adminclient.ErrorKindValidation:
				code = "CLI_VALIDATION_ERROR"
			case adminclient.ErrorKindNetwork:
				code = "NETWORK_ERROR"
			case adminclient.ErrorKindProtocol:
				code = "API_MALFORMED_RESPONSE"
			case adminclient.ErrorKindAPI:
				code = "API_ERROR"
			default:
				code = "CLI_ERROR"
			}
		}

		message := strings.TrimSpace(clientErr.Message)
		if message == "" {
			message = "command failed"
		}
		switch code {
		case "RETRY_NOT_ALLOWED":
			message = "retry is not allowed for the current task status"
		case "CANCEL_NOT_ALLOWED":
			message = "cancel is not allowed for the current task status"
		case "TASK_STATE_CONFLICT":
			message = "task state conflict"
		}
		return commandErrorInfo{
			Code:          code,
			Message:       message,
			CorrelationID: strings.TrimSpace(clientErr.CorrelationID),
		}
	}

	return commandErrorInfo{
		Code:    "CLI_ERROR",
		Message: "command failed",
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

const cliUsageText = `usage:
  followerctl tasks list [--output table|json]
  followerctl tasks get <task-id> [--output table|json]
  followerctl tasks retry <task-id> [--output table|json]
  followerctl tasks cancel <task-id> [--output table|json]
  followerctl tasks failures [--output table|json]`
