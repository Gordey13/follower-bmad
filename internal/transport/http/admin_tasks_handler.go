package httptransport

import (
	"context"
	stdhttp "net/http"
	"strings"

	"follower/internal/domain"
	"follower/internal/repository"

	"github.com/google/uuid"
)

// Admin API v1 envelope examples:
// Success: {"data":{"tasks":[]},"error":null,"meta":{}}
// Error: {"data":null,"error":{"code":"ADMIN_ENDPOINT_NOT_IMPLEMENTED","message":"admin endpoint is not implemented yet","details":{"endpoint":"GET /api/v1/tasks"}},"meta":{}}
type adminCSVQueueWriter interface {
	EnqueueValidatedBatch(
		ctx context.Context,
		rows []repository.EnqueueValidatedRow,
	) (repository.EnqueueValidatedBatchResult, error)
}

type adminTaskReader interface {
	GetByID(ctx context.Context, taskID uuid.UUID) (domain.Task, error)
}

type adminTaskFailuresReader interface {
	ListFailures(ctx context.Context, limit int, offset int) ([]domain.Task, error)
}

type adminTaskRetrier interface {
	RetryFromTask(ctx context.Context, sourceTaskID uuid.UUID) (domain.Task, error)
}

type adminTaskCanceler interface {
	CancelTask(ctx context.Context, taskID uuid.UUID, reason string) (domain.Task, error)
}

type adminResultReader interface {
	GetByTaskAttempt(ctx context.Context, taskID uuid.UUID, attempt int) (domain.FollowResult, error)
}

type adminTasksHandler struct {
	queueWriter    adminCSVQueueWriter
	taskReader     adminTaskReader
	failuresReader adminTaskFailuresReader
	retrier        adminTaskRetrier
	canceler       adminTaskCanceler
	resultReader   adminResultReader
}

func NewAdminTasksHandler(
	queueWriter adminCSVQueueWriter,
	taskReader adminTaskReader,
	resultReader adminResultReader,
) stdhttp.Handler {
	var failuresReader adminTaskFailuresReader
	var retrier adminTaskRetrier
	var canceler adminTaskCanceler
	if taskReader != nil {
		if reader, ok := taskReader.(adminTaskFailuresReader); ok {
			failuresReader = reader
		}
		if retryAdapter, ok := taskReader.(adminTaskRetrier); ok {
			retrier = retryAdapter
		}
		if cancelAdapter, ok := taskReader.(adminTaskCanceler); ok {
			canceler = cancelAdapter
		}
	}
	handler := adminTasksHandler{
		queueWriter:    queueWriter,
		taskReader:     taskReader,
		failuresReader: failuresReader,
		retrier:        retrier,
		canceler:       canceler,
		resultReader:   resultReader,
	}
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /tasks", handler.listTasks)
	mux.HandleFunc("GET /tasks/{id}", handler.getTask)
	mux.HandleFunc("POST /tasks/{id}/retry", handler.retryTask)
	mux.HandleFunc("POST /tasks/{id}/cancel", handler.cancelTask)
	mux.HandleFunc("GET /tasks/failures", handler.listFailures)
	mux.HandleFunc("POST /tasks:csv", handler.importCSV)
	return mux
}

func (adminTasksHandler) listTasks(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	writeAdminEndpointNotImplemented(w, "GET /api/v1/tasks")
}

func (handler adminTasksHandler) getTask(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	taskID, ok := parseAdminTaskID(w, r)
	if !ok {
		return
	}
	if handler.taskReader == nil {
		writeAdminEndpointNotAvailable(w, "GET /api/v1/tasks/{id}")
		return
	}

	task, err := handler.taskReader.GetByID(r.Context(), taskID)
	if err != nil {
		switch {
		case domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotFound):
			writeAdminErrorResponse(w, stdhttp.StatusNotFound, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskNotFound),
				Message: "task not found",
				Details: map[string]any{
					"task_id": taskID.String(),
				},
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskIdentifier):
			writeAdminErrorResponse(w, stdhttp.StatusBadRequest, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskIDInvalid),
				Message: "task id must be a valid uuid",
			})
		default:
			writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
				Code:    string(AdminErrorCodeInternalAdminAPIError),
				Message: "failed to fetch task detail",
			})
		}
		return
	}

	detail := newAdminTaskDetailDTO(task)
	if handler.resultReader != nil && task.Attempt > 0 {
		result, resultErr := handler.resultReader.GetByTaskAttempt(
			r.Context(),
			task.ID,
			task.Attempt,
		)
		switch {
		case resultErr == nil:
			detail.AttemptContext = newAdminTaskAttemptContextDTO(result)
		case domain.IsDomainErrorCode(resultErr, domain.ErrorCodeFollowResultNotFound):
			// Follow result is optional for task detail contract.
		default:
			writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
				Code:    string(AdminErrorCodeInternalAdminAPIError),
				Message: "failed to fetch task attempt context",
			})
			return
		}
	}

	writeAdminSuccessResponse(w, stdhttp.StatusOK, detail)
}

func (handler adminTasksHandler) retryTask(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	taskID, ok := parseAdminTaskID(w, r)
	if !ok {
		return
	}
	if handler.retrier == nil {
		writeAdminEndpointNotAvailable(w, "POST /api/v1/tasks/{id}/retry")
		return
	}

	retriedTask, err := handler.retrier.RetryFromTask(r.Context(), taskID)
	if err != nil {
		switch {
		case domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotFound):
			writeAdminErrorResponse(w, stdhttp.StatusNotFound, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskNotFound),
				Message: "task not found",
				Details: map[string]any{
					"task_id": taskID.String(),
				},
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeRetryNotAllowed):
			writeAdminErrorResponse(w, stdhttp.StatusConflict, adminErrorPayload{
				Code:    string(AdminErrorCodeRetryNotAllowed),
				Message: "retry is not allowed for the current task status",
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeTaskStateConflict),
			domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskTransition):
			writeAdminErrorResponse(w, stdhttp.StatusConflict, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskStateConflict),
				Message: "task state conflict",
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskIdentifier):
			writeAdminErrorResponse(w, stdhttp.StatusBadRequest, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskIDInvalid),
				Message: "task id must be a valid uuid",
			})
		default:
			writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
				Code:    string(AdminErrorCodeInternalAdminAPIError),
				Message: "failed to retry task",
			})
		}
		return
	}

	response := adminTaskRetryResponse{
		SourceTaskID: taskID.String(),
		NewTaskID:    retriedTask.ID.String(),
		Status:       string(retriedTask.Status),
	}
	correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if correlationID != "" {
		response.CorrelationID = &correlationID
	}

	writeAdminSuccessResponse(w, stdhttp.StatusOK, response)
}

func (handler adminTasksHandler) cancelTask(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	taskID, ok := parseAdminTaskID(w, r)
	if !ok {
		return
	}
	if handler.canceler == nil {
		writeAdminEndpointNotAvailable(w, "POST /api/v1/tasks/{id}/cancel")
		return
	}

	canceledTask, err := handler.canceler.CancelTask(r.Context(), taskID, "")
	if err != nil {
		switch {
		case domain.IsDomainErrorCode(err, domain.ErrorCodeTaskNotFound):
			writeAdminErrorResponse(w, stdhttp.StatusNotFound, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskNotFound),
				Message: "task not found",
				Details: map[string]any{
					"task_id": taskID.String(),
				},
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeCancelNotAllowed):
			writeAdminErrorResponse(w, stdhttp.StatusConflict, adminErrorPayload{
				Code:    string(AdminErrorCodeCancelNotAllowed),
				Message: "cancel is not allowed for the current task status",
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeTaskStateConflict),
			domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskTransition):
			writeAdminErrorResponse(w, stdhttp.StatusConflict, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskStateConflict),
				Message: "task state conflict",
			})
		case domain.IsDomainErrorCode(err, domain.ErrorCodeInvalidTaskIdentifier):
			writeAdminErrorResponse(w, stdhttp.StatusBadRequest, adminErrorPayload{
				Code:    string(AdminErrorCodeTaskIDInvalid),
				Message: "task id must be a valid uuid",
			})
		default:
			writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
				Code:    string(AdminErrorCodeInternalAdminAPIError),
				Message: "failed to cancel task",
			})
		}
		return
	}

	response := adminTaskCancelResponse{
		TaskID:       canceledTask.ID.String(),
		Status:       string(canceledTask.Status),
		ResultReason: strings.TrimSpace(canceledTask.ResultReason),
	}
	correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
	if correlationID != "" {
		response.CorrelationID = &correlationID
	}

	writeAdminSuccessResponse(w, stdhttp.StatusOK, response)
}

func (handler adminTasksHandler) listFailures(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if handler.failuresReader == nil {
		writeAdminEndpointNotAvailable(w, "GET /api/v1/tasks/failures")
		return
	}

	const (
		defaultLimit  = 200
		defaultOffset = 0
	)

	tasks, err := handler.failuresReader.ListFailures(r.Context(), defaultLimit, defaultOffset)
	if err != nil {
		writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
			Code:    string(AdminErrorCodeInternalAdminAPIError),
			Message: "failed to fetch triage tasks",
		})
		return
	}

	items := make([]adminTaskFailureDTO, 0, len(tasks))
	for _, task := range tasks {
		item := newAdminTaskFailureDTO(task)
		if handler.resultReader != nil && task.Attempt > 0 {
			result, resultErr := handler.resultReader.GetByTaskAttempt(
				r.Context(),
				task.ID,
				task.Attempt,
			)
			switch {
			case resultErr == nil:
				enrichAdminTaskFailureDTO(&item, result)
			case domain.IsDomainErrorCode(resultErr, domain.ErrorCodeFollowResultNotFound):
				// Follow result is optional for failures listing.
			default:
				writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
					Code:    string(AdminErrorCodeInternalAdminAPIError),
					Message: "failed to fetch task attempt context",
				})
				return
			}
		}
		items = append(items, item)
	}

	writeAdminSuccessResponse(w, stdhttp.StatusOK, adminTaskFailuresResponseDTO{
		Tasks: items,
	})
}

func (handler adminTasksHandler) importCSV(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	validator := newAdminCSVValidator()
	result, err := validator.Validate(r.Body)
	if err != nil {
		var schemaErr *adminCSVSchemaError
		if isAdminCSVSchemaError(err, &schemaErr) {
			details := schemaErr.Details()
			details["rows_total"] = 0
			details["rows_valid"] = 0
			details["rows_invalid"] = 0

			writeAdminErrorResponse(w, stdhttp.StatusBadRequest, adminErrorPayload{
				Code:    string(AdminErrorCodeCSVSchemaInvalid),
				Message: schemaErr.Error(),
				Details: details,
			})
			return
		}

		writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
			Code:    string(AdminErrorCodeInternalAdminAPIError),
			Message: "failed to validate CSV payload",
		})
		return
	}

	report := result.report()
	if result.RowsValid == 0 {
		writeAdminErrorResponse(w, stdhttp.StatusUnprocessableEntity, adminErrorPayload{
			Code:    string(AdminErrorCodeCSVRowInvalid),
			Message: "CSV payload contains no valid rows",
			Details: report,
		})
		return
	}

	if handler.queueWriter == nil {
		writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
			Code:    string(AdminErrorCodeInternalAdminAPIError),
			Message: "queue writer is not configured",
			Details: report,
		})
		return
	}

	validatedRows := make([]repository.EnqueueValidatedRow, 0, len(result.ValidRows))
	for _, row := range result.ValidRows {
		validatedRows = append(validatedRows, repository.EnqueueValidatedRow{
			Row:           row.Row,
			AccountID:     row.AccountID,
			TargetProfile: row.TargetProfile,
		})
	}

	batchResult, writeErr := handler.queueWriter.EnqueueValidatedBatch(r.Context(), validatedRows)
	if writeErr != nil {
		adminCode := AdminErrorCodeInternalAdminAPIError
		if domain.IsDomainErrorCode(writeErr, domain.ErrorCodeTaskQueueWriteFailed) {
			adminCode = AdminErrorCodeQueueWriteFailed
		}

		writeAdminErrorResponse(w, stdhttp.StatusInternalServerError, adminErrorPayload{
			Code:    string(adminCode),
			Message: "failed to persist queued tasks",
			Details: report,
		})
		return
	}

	writeAdminSuccessResponse(w, stdhttp.StatusOK, adminCSVImportReport{
		RowsTotal:   result.RowsTotal,
		RowsValid:   result.RowsValid,
		RowsInvalid: result.RowsInvalid,
		RowsCreated: batchResult.RowsCreated,
		RowsSkipped: batchResult.RowsSkipped,
		Summary: adminCSVImportCounters{
			Created: batchResult.RowsCreated,
			Skipped: batchResult.RowsSkipped,
			Errors:  result.RowsInvalid,
		},
		InvalidRows: result.InvalidRows,
		SkippedRows: batchResult.SkippedRows,
	})
}

func writeAdminEndpointNotImplemented(w stdhttp.ResponseWriter, endpoint string) {
	writeAdminErrorResponse(w, stdhttp.StatusNotImplemented, adminErrorPayload{
		Code:    string(AdminErrorCodeEndpointNotImplemented),
		Message: "admin endpoint is not implemented yet",
		Details: map[string]any{
			"endpoint": endpoint,
		},
	})
}

func writeAdminEndpointNotAvailable(w stdhttp.ResponseWriter, endpoint string) {
	writeAdminErrorResponse(w, stdhttp.StatusServiceUnavailable, adminErrorPayload{
		Code:    string(AdminErrorCodeEndpointTemporarilyClosed),
		Message: "admin endpoint is temporarily unavailable",
		Details: map[string]any{
			"endpoint": endpoint,
		},
	})
}

func parseAdminTaskID(w stdhttp.ResponseWriter, r *stdhttp.Request) (uuid.UUID, bool) {
	taskIDRaw := strings.TrimSpace(r.PathValue("id"))
	taskID, err := uuid.Parse(taskIDRaw)
	if err != nil {
		writeAdminErrorResponse(w, stdhttp.StatusBadRequest, adminErrorPayload{
			Code:    string(AdminErrorCodeTaskIDInvalid),
			Message: "task id must be a valid uuid",
		})
		return uuid.Nil, false
	}

	return taskID, true
}

type adminCSVImportReport struct {
	RowsTotal   int                            `json:"rows_total"`
	RowsValid   int                            `json:"rows_valid"`
	RowsInvalid int                            `json:"rows_invalid"`
	RowsCreated int                            `json:"rows_created"`
	RowsSkipped int                            `json:"rows_skipped"`
	Summary     adminCSVImportCounters         `json:"summary"`
	InvalidRows []adminCSVRowError             `json:"invalid_rows"`
	SkippedRows []repository.EnqueueSkippedRow `json:"skipped_rows"`
}

type adminTaskRetryResponse struct {
	SourceTaskID  string  `json:"source_task_id"`
	NewTaskID     string  `json:"new_task_id"`
	Status        string  `json:"status"`
	CorrelationID *string `json:"correlation_id,omitempty"`
}

type adminTaskCancelResponse struct {
	TaskID        string  `json:"task_id"`
	Status        string  `json:"status"`
	ResultReason  string  `json:"result_reason"`
	CorrelationID *string `json:"correlation_id,omitempty"`
}

type adminCSVImportCounters struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Errors  int `json:"errors"`
}
