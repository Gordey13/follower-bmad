package httptransport

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"strings"

	"follower/internal/domain"
)

type AdminErrorCode string

const (
	AdminErrorCodeTaskNotFound              AdminErrorCode = "TASK_NOT_FOUND"
	AdminErrorCodeTaskIDInvalid             AdminErrorCode = "TASK_ID_INVALID"
	AdminErrorCodeTaskStateConflict         AdminErrorCode = "TASK_STATE_CONFLICT"
	AdminErrorCodeRetryNotAllowed           AdminErrorCode = "RETRY_NOT_ALLOWED"
	AdminErrorCodeCancelNotAllowed          AdminErrorCode = "CANCEL_NOT_ALLOWED"
	AdminErrorCodeInternalAdminAPIError     AdminErrorCode = "INTERNAL_ADMIN_API_ERROR"
	AdminErrorCodeCSVSchemaInvalid          AdminErrorCode = "CSV_SCHEMA_INVALID"
	AdminErrorCodeCSVRowInvalid             AdminErrorCode = "CSV_ROW_INVALID"
	AdminErrorCodeQueueWriteFailed          AdminErrorCode = "QUEUE_WRITE_FAILED"
	AdminErrorCodeEndpointNotImplemented    AdminErrorCode = "ADMIN_ENDPOINT_NOT_IMPLEMENTED"
	AdminErrorCodeEndpointTemporarilyClosed AdminErrorCode = "ADMIN_ENDPOINT_NOT_AVAILABLE"
)

type adminResponseEnvelope struct {
	Data  any                `json:"data"`
	Error *adminErrorPayload `json:"error"`
	Meta  adminMetaEnvelope  `json:"meta"`
}

type adminErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type adminMetaEnvelope struct {
	CorrelationID string `json:"correlation_id,omitempty"`
}

func writeAdminSuccessResponse(w stdhttp.ResponseWriter, statusCode int, data any) {
	writeAdminSuccessResponseWithMeta(w, statusCode, data, adminMetaEnvelope{})
}

func writeAdminSuccessResponseWithMeta(
	w stdhttp.ResponseWriter,
	statusCode int,
	data any,
	meta adminMetaEnvelope,
) {
	annotateAdminResponseTelemetry(w, "success", "none")
	writeAdminEnvelope(w, statusCode, adminResponseEnvelope{
		Data:  data,
		Error: nil,
		Meta:  meta,
	})
}

func writeAdminErrorResponse(w stdhttp.ResponseWriter, statusCode int, payload adminErrorPayload) {
	writeAdminErrorResponseWithMeta(w, statusCode, payload, adminMetaEnvelope{})
}

func writeAdminErrorResponseWithMeta(
	w stdhttp.ResponseWriter,
	statusCode int,
	payload adminErrorPayload,
	meta adminMetaEnvelope,
) {
	if strings.TrimSpace(payload.Code) == "" {
		payload.Code = string(AdminErrorCodeInternalAdminAPIError)
	}
	if strings.TrimSpace(payload.Message) == "" {
		payload.Message = "internal admin API error"
	}

	annotateAdminResponseTelemetry(w, "error", payload.Code)

	writeAdminEnvelope(w, statusCode, adminResponseEnvelope{
		Data:  nil,
		Error: &payload,
		Meta:  meta,
	})
}

func writeAdminEnvelope(w stdhttp.ResponseWriter, statusCode int, envelope adminResponseEnvelope) {
	if strings.TrimSpace(envelope.Meta.CorrelationID) == "" {
		envelope.Meta.CorrelationID = strings.TrimSpace(w.Header().Get(correlationIDHeader))
	}
	if strings.TrimSpace(envelope.Meta.CorrelationID) != "" {
		w.Header().Set(correlationIDHeader, envelope.Meta.CorrelationID)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(envelope)
}

func mapAdminErrorCode(err error) AdminErrorCode {
	var domainErr *domain.DomainError
	if !errors.As(err, &domainErr) {
		return AdminErrorCodeInternalAdminAPIError
	}

	switch domainErr.Code {
	case domain.ErrorCodeTaskNotFound:
		return AdminErrorCodeTaskNotFound
	case domain.ErrorCodeInvalidTaskIdentifier:
		return AdminErrorCodeTaskIDInvalid
	case domain.ErrorCodeRetryNotAllowed:
		return AdminErrorCodeRetryNotAllowed
	case domain.ErrorCodeCancelNotAllowed:
		return AdminErrorCodeCancelNotAllowed
	case domain.ErrorCodeTaskNotRunning,
		domain.ErrorCodeTaskClaimOwnerMismatch,
		domain.ErrorCodeTaskStateConflict,
		domain.ErrorCodeInvalidTaskTransition:
		return AdminErrorCodeTaskStateConflict
	default:
		return AdminErrorCodeInternalAdminAPIError
	}
}
