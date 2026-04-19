package httptransport

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"follower/internal/domain"
)

func TestWriteAdminSuccessResponseUsesUnifiedEnvelope(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeAdminSuccessResponse(rec, stdhttp.StatusOK, map[string]any{
		"task_id": "task-123",
	})

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	var envelope adminResponseEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if envelope.Data == nil {
		t.Fatal("expected data field to be present")
	}
	if envelope.Error != nil {
		t.Fatalf("expected error field to be nil for success response, got %+v", envelope.Error)
	}
}

func TestWriteAdminErrorResponseUsesDeterministicCode(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeAdminErrorResponse(rec, stdhttp.StatusNotImplemented, adminErrorPayload{
		Code:    string(AdminErrorCodeEndpointNotImplemented),
		Message: "endpoint is not implemented",
		Details: map[string]any{
			"endpoint": "GET /api/v1/tasks",
		},
	})

	if rec.Code != stdhttp.StatusNotImplemented {
		t.Fatalf("expected status 501, got %d", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("expected JSON content type, got %q", got)
	}

	var envelope adminResponseEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if envelope.Error == nil {
		t.Fatal("expected error field to be present")
	}
	if envelope.Error.Code != string(AdminErrorCodeEndpointNotImplemented) {
		t.Fatalf("expected error code %q, got %q", AdminErrorCodeEndpointNotImplemented, envelope.Error.Code)
	}
	if envelope.Error.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestMapAdminErrorCodeUsesBoundedVocabulary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want AdminErrorCode
	}{
		{
			name: "task not found",
			err:  domain.NewDomainError(domain.ErrorCodeTaskNotFound, "task is missing"),
			want: AdminErrorCodeTaskNotFound,
		},
		{
			name: "task state conflict",
			err:  domain.NewDomainError(domain.ErrorCodeTaskNotRunning, "task state mismatch"),
			want: AdminErrorCodeTaskStateConflict,
		},
		{
			name: "task id invalid",
			err:  domain.NewDomainError(domain.ErrorCodeInvalidTaskIdentifier, "task id invalid"),
			want: AdminErrorCodeTaskIDInvalid,
		},
		{
			name: "retry not allowed",
			err:  domain.NewDomainError(domain.ErrorCodeRetryNotAllowed, "retry denied"),
			want: AdminErrorCodeRetryNotAllowed,
		},
		{
			name: "cancel not allowed",
			err:  domain.NewDomainError(domain.ErrorCodeCancelNotAllowed, "cancel denied"),
			want: AdminErrorCodeCancelNotAllowed,
		},
		{
			name: "fallback internal",
			err:  errors.New("unexpected"),
			want: AdminErrorCodeInternalAdminAPIError,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := mapAdminErrorCode(tc.err); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestWriteAdminSuccessResponseWithMetaIncludesCorrelationID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writeAdminSuccessResponseWithMeta(
		rec,
		stdhttp.StatusOK,
		map[string]any{"task_id": "task-123"},
		adminMetaEnvelope{CorrelationID: "corr-meta-001"},
	)

	var envelope struct {
		Data  map[string]any     `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  struct {
			CorrelationID string `json:"correlation_id"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if envelope.Meta.CorrelationID != "corr-meta-001" {
		t.Fatalf("expected meta.correlation_id corr-meta-001, got %q", envelope.Meta.CorrelationID)
	}
}
