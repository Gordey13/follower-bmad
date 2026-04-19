package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"follower/internal/domain"
	"follower/internal/repository"

	"github.com/google/uuid"
)

func TestAdminTaskSkeletonEndpointsReturnStandardizedEnvelope(t *testing.T) {
	t.Parallel()

	healthHandler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	metricsHandler := stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = io.WriteString(w, "metrics")
	})

	server := httptest.NewServer(NewServer(ServerConfig{Address: ":0"}, healthHandler, metricsHandler).Handler)
	defer server.Close()

	tests := []struct {
		name      string
		method    string
		path      string
		status    int
		errorCode string
	}{
		{name: "tasks list", method: stdhttp.MethodGet, path: "/api/v1/tasks", status: stdhttp.StatusNotImplemented, errorCode: string(AdminErrorCodeEndpointNotImplemented)},
		{name: "task retry", method: stdhttp.MethodPost, path: "/api/v1/tasks/00000000-0000-0000-0000-000000000001/retry", status: stdhttp.StatusServiceUnavailable, errorCode: string(AdminErrorCodeEndpointTemporarilyClosed)},
		{name: "task cancel", method: stdhttp.MethodPost, path: "/api/v1/tasks/00000000-0000-0000-0000-000000000001/cancel", status: stdhttp.StatusServiceUnavailable, errorCode: string(AdminErrorCodeEndpointTemporarilyClosed)},
		{name: "task failures", method: stdhttp.MethodGet, path: "/api/v1/tasks/failures", status: stdhttp.StatusServiceUnavailable, errorCode: string(AdminErrorCodeEndpointTemporarilyClosed)},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req, err := stdhttp.NewRequest(tc.method, server.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("create request %s %s: %v", tc.method, tc.path, err)
			}

			resp, err := stdhttp.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request %s %s: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.status {
				t.Fatalf("expected status %d, got %d", tc.status, resp.StatusCode)
			}

			var envelope adminResponseEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode response body: %v", err)
			}

			if envelope.Error == nil {
				t.Fatal("expected error object in envelope")
			}
			if envelope.Error.Code != tc.errorCode {
				t.Fatalf("expected error code %q, got %q", tc.errorCode, envelope.Error.Code)
			}
		})
	}
}

func TestAdminTaskDetailEndpointReturnsValidationErrorForInvalidID(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/not-a-uuid")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/not-a-uuid failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskIDInvalid) {
		t.Fatalf("expected error code %q, got %q", AdminErrorCodeTaskIDInvalid, envelope.Error.Code)
	}
	if taskReader.calls != 0 {
		t.Fatalf("expected task reader not called for invalid id, got %d calls", taskReader.calls)
	}
}

func TestAdminTaskDetailEndpointReturnsTaskDetailWithAttemptContext(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	claimedAt := time.Now().UTC().Truncate(time.Second)
	startedAt := claimedAt.Add(2 * time.Second)
	createdAt := claimedAt.Add(-5 * time.Minute)
	updatedAt := startedAt.Add(30 * time.Second)

	taskReader := &fakeAdminTaskReader{
		task: domain.Task{
			ID:            taskID,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/1001",
			Status:        domain.TaskStatusRunning,
			Attempt:       2,
			ClaimedBy:     "worker-1",
			ClaimedAt:     &claimedAt,
			StartedAt:     &startedAt,
			FinishedAt:    nil,
			ErrorCode:     "",
			ResultReason:  "",
			CreatedAt:     createdAt,
			UpdatedAt:     updatedAt,
		},
	}
	resultReader := &fakeAdminResultReader{
		result: domain.FollowResult{
			TaskID:              taskID,
			AccountID:           accountID,
			TargetProfile:       "https://oskelly.ru/profile/1001",
			Attempt:             2,
			Outcome:             domain.FollowFlowOutcomeNavigationFailed,
			Verified:            false,
			VerificationSignal:  domain.FollowVerificationSignalNavigationFailed,
			VerificationReason:  "navigation timeout",
			ErrorCode:           domain.ErrorCodeFollowNavigationFailed,
			ScreenshotObjectKey: "screenshots/task.png",
			ArtifactObjectKeys:  []string{"artifacts/execution.json"},
		},
	}

	server := newAdminTaskServerWithDependencies(t, nil, taskReader, resultReader)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/" + taskID.String())
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data  adminTaskDetailDTO `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if envelope.Data.ID != taskID.String() {
		t.Fatalf("expected task id %s, got %s", taskID.String(), envelope.Data.ID)
	}
	if envelope.Data.AccountID != accountID.String() {
		t.Fatalf("expected account id %s, got %s", accountID.String(), envelope.Data.AccountID)
	}
	if envelope.Data.Attempt != 2 {
		t.Fatalf("expected attempt 2, got %d", envelope.Data.Attempt)
	}
	if envelope.Data.ClaimedBy == nil || *envelope.Data.ClaimedBy != "worker-1" {
		t.Fatalf("expected claimed_by worker-1, got %+v", envelope.Data.ClaimedBy)
	}
	if envelope.Data.AttemptContext == nil {
		t.Fatal("expected attempt context to be present")
	}
	if envelope.Data.AttemptContext.Outcome != string(domain.FollowFlowOutcomeNavigationFailed) {
		t.Fatalf("expected follow outcome %s, got %s", domain.FollowFlowOutcomeNavigationFailed, envelope.Data.AttemptContext.Outcome)
	}
	if len(envelope.Data.AttemptContext.ArtifactObjectKeys) != 1 {
		t.Fatalf("expected 1 artifact key, got %d", len(envelope.Data.AttemptContext.ArtifactObjectKeys))
	}
	if resultReader.calls != 1 {
		t.Fatalf("expected result reader to be called once, got %d", resultReader.calls)
	}
	if resultReader.gotAttempt != 2 {
		t.Fatalf("expected result reader attempt=2, got %d", resultReader.gotAttempt)
	}
}

func TestAdminTaskDetailEndpointReturnsNullAttemptContextWhenFollowResultMissing(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	accountID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	taskReader := &fakeAdminTaskReader{
		task: domain.Task{
			ID:            taskID,
			AccountID:     accountID,
			TargetProfile: "https://oskelly.ru/profile/1002",
			Status:        domain.TaskStatusRunning,
			Attempt:       1,
			CreatedAt:     now.Add(-time.Minute),
			UpdatedAt:     now,
		},
	}
	resultReader := &fakeAdminResultReader{
		err: domain.NewDomainError(domain.ErrorCodeFollowResultNotFound, "follow result missing"),
	}

	server := newAdminTaskServerWithDependencies(t, nil, taskReader, resultReader)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/" + taskID.String())
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data  adminTaskDetailDTO `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if envelope.Data.AttemptContext != nil {
		t.Fatalf("expected attempt_context=null when follow result is missing, got %+v", envelope.Data.AttemptContext)
	}
}

func TestAdminTaskDetailEndpointReturnsTaskNotFoundError(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	taskReader := &fakeAdminTaskReader{
		err: domain.NewDomainError(domain.ErrorCodeTaskNotFound, "task is missing"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/" + taskID.String())
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskNotFound) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeTaskNotFound, envelope.Error.Code)
	}
}

func TestAdminTaskDetailEndpointSanitizesUnexpectedRepositoryError(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	taskReader := &fakeAdminTaskReader{
		err: errors.New("sql connect failed secret=token-123"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/" + taskID.String())
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/{id} failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeInternalAdminAPIError) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeInternalAdminAPIError, envelope.Error.Code)
	}
	if strings.Contains(strings.ToLower(envelope.Error.Message), "secret") {
		t.Fatalf("unexpected secret in error message: %q", envelope.Error.Message)
	}
}

func TestAdminTaskFailuresEndpointReturnsTriageTasksWithOptionalEnrichment(t *testing.T) {
	t.Parallel()

	taskFailID := uuid.New()
	taskRetryID := uuid.New()
	accountID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	taskReader := &fakeAdminTaskReader{
		failures: []domain.Task{
			{
				ID:            taskFailID,
				AccountID:     accountID,
				TargetProfile: "https://oskelly.ru/profile/fail",
				Status:        domain.TaskStatusFail,
				Attempt:       2,
				ErrorCode:     domain.ErrorCodeFollowTargetUnreachable,
				ResultReason:  "target unreachable",
				UpdatedAt:     now,
			},
			{
				ID:            taskRetryID,
				AccountID:     accountID,
				TargetProfile: "https://oskelly.ru/profile/retry",
				Status:        domain.TaskStatusRetry,
				Attempt:       1,
				ErrorCode:     domain.ErrorCodeFollowNavigationFailed,
				ResultReason:  "navigation timeout",
				UpdatedAt:     now.Add(-time.Minute),
			},
		},
	}

	resultReader := &fakeAdminResultReader{
		resultByTaskAttempt: map[string]domain.FollowResult{
			taskFailID.String() + "#2": {
				TaskID:             taskFailID,
				AccountID:          accountID,
				TargetProfile:      "https://oskelly.ru/profile/fail",
				Attempt:            2,
				Outcome:            domain.FollowFlowOutcomeTargetUnreachable,
				VerificationSignal: domain.FollowVerificationSignalTargetUnreachable,
			},
		},
		errByTaskAttempt: map[string]error{
			taskRetryID.String() + "#1": domain.NewDomainError(
				domain.ErrorCodeFollowResultNotFound,
				"follow result missing",
			),
		},
	}

	server := newAdminTaskServerWithDependencies(t, nil, taskReader, resultReader)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/failures")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/failures failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			Tasks []adminTaskFailureDTO `json:"tasks"`
		} `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if len(envelope.Data.Tasks) != 2 {
		t.Fatalf("expected 2 failure tasks, got %d", len(envelope.Data.Tasks))
	}

	first := envelope.Data.Tasks[0]
	if first.Status != string(domain.TaskStatusFail) {
		t.Fatalf("expected first status %s, got %s", domain.TaskStatusFail, first.Status)
	}
	if first.FollowOutcome == nil || *first.FollowOutcome != string(domain.FollowFlowOutcomeTargetUnreachable) {
		t.Fatalf("expected enriched follow_outcome, got %+v", first.FollowOutcome)
	}
	if first.VerificationSignal == nil || *first.VerificationSignal != string(domain.FollowVerificationSignalTargetUnreachable) {
		t.Fatalf("expected enriched verification_signal, got %+v", first.VerificationSignal)
	}

	second := envelope.Data.Tasks[1]
	if second.Status != string(domain.TaskStatusRetry) {
		t.Fatalf("expected second status %s, got %s", domain.TaskStatusRetry, second.Status)
	}
	if second.FollowOutcome != nil || second.VerificationSignal != nil {
		t.Fatalf("expected nil enrichment for missing follow result, got %+v", second)
	}

	if taskReader.listFailuresCalls != 1 {
		t.Fatalf("expected ListFailures to be called once, got %d", taskReader.listFailuresCalls)
	}
	if taskReader.lastLimit != 200 || taskReader.lastOffset != 0 {
		t.Fatalf("expected default pagination limit/offset 200/0, got %d/%d", taskReader.lastLimit, taskReader.lastOffset)
	}
}

func TestAdminTaskFailuresEndpointReturnsEmptyList(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{failures: []domain.Task{}}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/failures")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/failures failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			Tasks []adminTaskFailureDTO `json:"tasks"`
		} `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if len(envelope.Data.Tasks) != 0 {
		t.Fatalf("expected empty tasks list, got %d", len(envelope.Data.Tasks))
	}
}

func TestAdminTaskFailuresEndpointReturnsInternalErrorWhenRepositoryFails(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		listFailuresErr: errors.New("db timeout secret=abc"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Get(server.URL + "/api/v1/tasks/failures")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/failures failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeInternalAdminAPIError) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeInternalAdminAPIError, envelope.Error.Code)
	}
	if strings.Contains(strings.ToLower(envelope.Error.Message), "secret") {
		t.Fatalf("unexpected secret in error message: %q", envelope.Error.Message)
	}
}

func TestAdminTaskFailuresRouteDoesNotConflictWithTaskDetailRoute(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		failures: []domain.Task{},
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	failuresResp, err := stdhttp.Get(server.URL + "/api/v1/tasks/failures")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/failures failed: %v", err)
	}
	defer failuresResp.Body.Close()
	if failuresResp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected failures status 200, got %d", failuresResp.StatusCode)
	}
	if taskReader.listFailuresCalls != 1 {
		t.Fatalf("expected ListFailures call count 1, got %d", taskReader.listFailuresCalls)
	}
	if taskReader.calls != 0 {
		t.Fatalf("expected GetByID not called by failures route, got %d", taskReader.calls)
	}

	detailResp, err := stdhttp.Get(server.URL + "/api/v1/tasks/not-a-uuid")
	if err != nil {
		t.Fatalf("GET /api/v1/tasks/not-a-uuid failed: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected task detail invalid-id status 400, got %d", detailResp.StatusCode)
	}
}

func TestAdminTaskRetryEndpointCreatesLinkedQueuedTask(t *testing.T) {
	t.Parallel()

	sourceID := uuid.New()
	newID := uuid.New()
	sourceRef := sourceID
	taskReader := &fakeAdminTaskReader{
		retryTask: domain.Task{
			ID:            newID,
			SourceTaskID:  &sourceRef,
			AccountID:     uuid.New(),
			TargetProfile: "https://oskelly.ru/profile/retry-source",
			Status:        domain.TaskStatusQueued,
		},
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	req, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v1/tasks/"+sourceID.String()+"/retry", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Correlation-ID", "corr-retry-001")
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			SourceTaskID  string  `json:"source_task_id"`
			NewTaskID     string  `json:"new_task_id"`
			Status        string  `json:"status"`
			CorrelationID *string `json:"correlation_id"`
		} `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if envelope.Data.SourceTaskID != sourceID.String() {
		t.Fatalf("expected source_task_id=%s, got %s", sourceID.String(), envelope.Data.SourceTaskID)
	}
	if envelope.Data.NewTaskID != newID.String() {
		t.Fatalf("expected new_task_id=%s, got %s", newID.String(), envelope.Data.NewTaskID)
	}
	if envelope.Data.Status != string(domain.TaskStatusQueued) {
		t.Fatalf("expected status=%s, got %s", domain.TaskStatusQueued, envelope.Data.Status)
	}
	if envelope.Data.CorrelationID == nil || *envelope.Data.CorrelationID != "corr-retry-001" {
		t.Fatalf("expected correlation_id corr-retry-001, got %+v", envelope.Data.CorrelationID)
	}
	if taskReader.retryCalls != 1 {
		t.Fatalf("expected RetryFromTask call count 1, got %d", taskReader.retryCalls)
	}
	if taskReader.retrySourceID != sourceID {
		t.Fatalf("expected RetryFromTask source id %s, got %s", sourceID.String(), taskReader.retrySourceID.String())
	}
}

func TestAdminTaskRetryEndpointEchoesCorrelationIDInHeaderAndMeta(t *testing.T) {
	t.Parallel()

	sourceID := uuid.New()
	newID := uuid.New()
	taskReader := &fakeAdminTaskReader{
		retryTask: domain.Task{
			ID:            newID,
			AccountID:     uuid.New(),
			TargetProfile: "https://oskelly.ru/profile/retry-correlation",
			Status:        domain.TaskStatusQueued,
		},
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	req, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v1/tasks/"+sourceID.String()+"/retry", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Correlation-ID", "corr-retry-meta-001")
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Correlation-ID"); got != "corr-retry-meta-001" {
		t.Fatalf("expected response header X-Correlation-ID=corr-retry-meta-001, got %q", got)
	}

	var envelope struct {
		Data  map[string]any     `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  struct {
			CorrelationID string `json:"correlation_id"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Meta.CorrelationID != "corr-retry-meta-001" {
		t.Fatalf("expected meta.correlation_id corr-retry-meta-001, got %q", envelope.Meta.CorrelationID)
	}
}

func TestAdminTaskRetryEndpointGeneratesCorrelationIDWhenMissing(t *testing.T) {
	t.Parallel()

	sourceID := uuid.New()
	newID := uuid.New()
	taskReader := &fakeAdminTaskReader{
		retryTask: domain.Task{
			ID:            newID,
			AccountID:     uuid.New(),
			TargetProfile: "https://oskelly.ru/profile/retry-generated-correlation",
			Status:        domain.TaskStatusQueued,
		},
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	req, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v1/tasks/"+sourceID.String()+"/retry", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	correlationID := strings.TrimSpace(resp.Header.Get("X-Correlation-ID"))
	if correlationID == "" {
		t.Fatal("expected generated X-Correlation-ID header to be non-empty")
	}
	if _, err := uuid.Parse(correlationID); err != nil {
		t.Fatalf("expected generated correlation id to be UUID, got %q (err=%v)", correlationID, err)
	}

	var envelope struct {
		Data  map[string]any     `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  struct {
			CorrelationID string `json:"correlation_id"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Meta.CorrelationID != correlationID {
		t.Fatalf("expected meta.correlation_id=%q, got %q", correlationID, envelope.Meta.CorrelationID)
	}
}

func TestAdminCSVEndpointEmitsStructuredOperationLogWithRequiredFields(t *testing.T) {
	t.Parallel()

	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, nil))
	server := httptest.NewServer(NewServer(
		ServerConfig{Address: ":0"},
		stdhttp.NotFoundHandler(),
		stdhttp.NotFoundHandler(),
		NewAdminTasksHandler(nil, nil, nil, WithAdminLogger(logger)),
	).Handler)
	defer server.Close()

	req, err := stdhttp.NewRequest(
		stdhttp.MethodPost,
		server.URL+"/api/v1/tasks:csv",
		strings.NewReader("account,target_profile\n"),
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Correlation-ID", "corr-csv-log-001")
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks:csv failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	logOutput := logBuffer.String()
	required := []string{
		"admin.csv_import",
		"correlation_id",
		"admin.action",
		"task_id",
		"operation.result",
		"http.route",
		"http.status_code",
		"error_code",
	}
	for _, want := range required {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected log output to contain %q, got:\n%s", want, logOutput)
		}
	}
}

func TestAdminTaskRetryEndpointReturnsTaskIDValidationError(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/not-a-uuid/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/not-a-uuid/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskIDInvalid) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeTaskIDInvalid, envelope.Error.Code)
	}
	if taskReader.retryCalls != 0 {
		t.Fatalf("expected RetryFromTask not called on invalid id, got %d calls", taskReader.retryCalls)
	}
}

func TestAdminTaskRetryEndpointReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		retryErr: domain.NewDomainError(domain.ErrorCodeTaskNotFound, "task missing"),
	}
	sourceID := uuid.New()
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+sourceID.String()+"/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskNotFound) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeTaskNotFound, envelope.Error.Code)
	}
}

func TestAdminTaskRetryEndpointReturnsRetryNotAllowed(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		retryErr: domain.NewDomainError(domain.ErrorCodeRetryNotAllowed, "retry denied"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+uuid.NewString()+"/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusConflict {
		t.Fatalf("expected status 409, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeRetryNotAllowed) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeRetryNotAllowed, envelope.Error.Code)
	}
}

func TestAdminTaskRetryEndpointSanitizesInternalErrors(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		retryErr: errors.New("db failure secret=token-123"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+uuid.NewString()+"/retry", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/retry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeInternalAdminAPIError) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeInternalAdminAPIError, envelope.Error.Code)
	}
	if strings.Contains(strings.ToLower(envelope.Error.Message), "secret") {
		t.Fatalf("unexpected secret in error message: %q", envelope.Error.Message)
	}
}

func TestAdminTaskCancelEndpointReturnsCanceledTaskContract(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	taskReader := &fakeAdminTaskReader{
		cancelTask: domain.Task{
			ID:           taskID,
			Status:       domain.TaskStatusCanceled,
			ResultReason: "task canceled by admin operator",
		},
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	req, err := stdhttp.NewRequest(stdhttp.MethodPost, server.URL+"/api/v1/tasks/"+taskID.String()+"/cancel", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Correlation-ID", "corr-cancel-001")
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/cancel failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			TaskID        string  `json:"task_id"`
			Status        string  `json:"status"`
			ResultReason  string  `json:"result_reason"`
			CorrelationID *string `json:"correlation_id"`
		} `json:"data"`
		Error *adminErrorPayload `json:"error"`
		Meta  adminMetaEnvelope  `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if envelope.Data.TaskID != taskID.String() {
		t.Fatalf("expected task_id %s, got %s", taskID.String(), envelope.Data.TaskID)
	}
	if envelope.Data.Status != string(domain.TaskStatusCanceled) {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusCanceled, envelope.Data.Status)
	}
	if envelope.Data.ResultReason != "task canceled by admin operator" {
		t.Fatalf("expected result_reason to be returned, got %q", envelope.Data.ResultReason)
	}
	if envelope.Data.CorrelationID == nil || *envelope.Data.CorrelationID != "corr-cancel-001" {
		t.Fatalf("expected correlation_id corr-cancel-001, got %+v", envelope.Data.CorrelationID)
	}
	if taskReader.cancelCalls != 1 {
		t.Fatalf("expected CancelTask call count 1, got %d", taskReader.cancelCalls)
	}
	if taskReader.cancelTaskID != taskID {
		t.Fatalf("expected CancelTask task id %s, got %s", taskID.String(), taskReader.cancelTaskID.String())
	}
}

func TestAdminTaskCancelEndpointReturnsTaskIDValidationError(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/not-a-uuid/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/not-a-uuid/cancel failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskIDInvalid) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeTaskIDInvalid, envelope.Error.Code)
	}
	if taskReader.cancelCalls != 0 {
		t.Fatalf("expected CancelTask not called on invalid id, got %d calls", taskReader.cancelCalls)
	}
}

func TestAdminTaskCancelEndpointReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		cancelErr: domain.NewDomainError(domain.ErrorCodeTaskNotFound, "task missing"),
	}
	taskID := uuid.New()
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+taskID.String()+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/cancel failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeTaskNotFound) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeTaskNotFound, envelope.Error.Code)
	}
}

func TestAdminTaskCancelEndpointReturnsConflictCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "cancel not allowed",
			err:  domain.NewDomainError(domain.ErrorCodeCancelNotAllowed, "cancel denied"),
			want: string(AdminErrorCodeCancelNotAllowed),
		},
		{
			name: "task state conflict",
			err:  domain.NewDomainError(domain.ErrorCodeInvalidTaskTransition, "transition denied"),
			want: string(AdminErrorCodeTaskStateConflict),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			taskReader := &fakeAdminTaskReader{
				cancelErr: tc.err,
			}
			server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
			defer server.Close()

			resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+uuid.NewString()+"/cancel", "application/json", nil)
			if err != nil {
				t.Fatalf("POST /api/v1/tasks/{id}/cancel failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != stdhttp.StatusConflict {
				t.Fatalf("expected status 409, got %d", resp.StatusCode)
			}

			var envelope adminResponseEnvelope
			if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if envelope.Error == nil {
				t.Fatal("expected error envelope")
			}
			if envelope.Error.Code != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, envelope.Error.Code)
			}
		})
	}
}

func TestAdminTaskCancelEndpointSanitizesInternalErrors(t *testing.T) {
	t.Parallel()

	taskReader := &fakeAdminTaskReader{
		cancelErr: errors.New("db failure secret=token-987"),
	}
	server := newAdminTaskServerWithDependencies(t, nil, taskReader, nil)
	defer server.Close()

	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks/"+uuid.NewString()+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/v1/tasks/{id}/cancel failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error envelope")
	}
	if envelope.Error.Code != string(AdminErrorCodeInternalAdminAPIError) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeInternalAdminAPIError, envelope.Error.Code)
	}
	if strings.Contains(strings.ToLower(envelope.Error.Message), "secret") {
		t.Fatalf("unexpected secret in error message: %q", envelope.Error.Message)
	}
}

func TestAdminCSVEndpointReturnsSchemaErrorForInvalidHeader(t *testing.T) {
	t.Parallel()

	server := newAdminTaskServer(t, nil)
	defer server.Close()

	body := "account,target_profile\n00000000-0000-0000-0000-000000000001,https://oskelly.ru/profile/1001\n"
	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks:csv", "text/csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/v1/tasks:csv failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error object")
	}
	if envelope.Error.Code != string(AdminErrorCodeCSVSchemaInvalid) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeCSVSchemaInvalid, envelope.Error.Code)
	}
}

func TestAdminCSVEndpointReturnsImportSummaryWithCreatedSkippedAndErrors(t *testing.T) {
	t.Parallel()

	writer := &fakeAdminBatchWriter{
		result: repository.EnqueueValidatedBatchResult{
			RowsCreated: 1,
			RowsSkipped: 1,
			SkippedRows: []repository.EnqueueSkippedRow{
				{
					Row:     4,
					Code:    "duplicate_active_task",
					Message: "active queued/running task already exists for account_id+target_profile",
				},
			},
		},
	}
	server := newAdminTaskServer(t, writer)
	defer server.Close()

	body := strings.Join([]string{
		"account_id,target_profile",
		"00000000-0000-0000-0000-000000000001,https://oskelly.ru/profile/1001",
		"bad-uuid,https://oskelly.ru/profile/1002",
		"00000000-0000-0000-0000-000000000003,https://oskelly.ru/profile/1003",
	}, "\n")
	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks:csv", "text/csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/v1/tasks:csv failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var envelope struct {
		Data struct {
			RowsTotal   int `json:"rows_total"`
			RowsValid   int `json:"rows_valid"`
			RowsInvalid int `json:"rows_invalid"`
			RowsCreated int `json:"rows_created"`
			RowsSkipped int `json:"rows_skipped"`
			Summary     struct {
				Created int `json:"created"`
				Skipped int `json:"skipped"`
				Errors  int `json:"errors"`
			} `json:"summary"`
			InvalidRows []adminCSVRowError `json:"invalid_rows"`
			SkippedRows []struct {
				Row  int    `json:"row"`
				Code string `json:"code"`
			} `json:"skipped_rows"`
		} `json:"data"`
		Error *adminErrorPayload `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	if envelope.Error != nil {
		t.Fatalf("expected success envelope, got error %+v", envelope.Error)
	}
	if envelope.Data.RowsTotal != 3 || envelope.Data.RowsValid != 2 || envelope.Data.RowsInvalid != 1 {
		t.Fatalf("unexpected row counters: %+v", envelope.Data)
	}
	if envelope.Data.RowsCreated != 1 || envelope.Data.RowsSkipped != 1 {
		t.Fatalf("unexpected write counters: %+v", envelope.Data)
	}
	if envelope.Data.Summary.Created != 1 || envelope.Data.Summary.Skipped != 1 || envelope.Data.Summary.Errors != 1 {
		t.Fatalf("unexpected summary counters: %+v", envelope.Data.Summary)
	}
	if len(envelope.Data.InvalidRows) != 1 {
		t.Fatalf("expected 1 invalid row, got %d", len(envelope.Data.InvalidRows))
	}
	if len(envelope.Data.SkippedRows) != 1 {
		t.Fatalf("expected 1 skipped row, got %d", len(envelope.Data.SkippedRows))
	}
	if envelope.Data.SkippedRows[0].Code != "duplicate_active_task" {
		t.Fatalf("expected duplicate_active_task skip reason, got %q", envelope.Data.SkippedRows[0].Code)
	}
	if len(writer.receivedRows) != 2 {
		t.Fatalf("expected writer to receive 2 valid rows, got %d", len(writer.receivedRows))
	}
}

func TestAdminCSVEndpointReturnsDeterministicErrorForInvalidOnlyPayload(t *testing.T) {
	t.Parallel()

	writer := &fakeAdminBatchWriter{}
	server := newAdminTaskServer(t, writer)
	defer server.Close()

	body := strings.Join([]string{
		"account_id,target_profile",
		"bad-uuid,https://oskelly.ru/profiles/invalid",
	}, "\n")
	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks:csv", "text/csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/v1/tasks:csv failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error object")
	}
	if envelope.Error.Code != string(AdminErrorCodeCSVRowInvalid) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeCSVRowInvalid, envelope.Error.Code)
	}
	if len(writer.receivedRows) != 0 {
		t.Fatalf("expected writer not to be called for invalid-only payload, got %d rows", len(writer.receivedRows))
	}
}

func TestAdminCSVEndpointReturnsQueueWriteFailedCode(t *testing.T) {
	t.Parallel()

	writer := &fakeAdminBatchWriter{
		err: domain.NewDomainError(domain.ErrorCodeTaskQueueWriteFailed, "batch insert failed"),
	}
	server := newAdminTaskServer(t, writer)
	defer server.Close()

	body := strings.Join([]string{
		"account_id,target_profile",
		"00000000-0000-0000-0000-000000000001,https://oskelly.ru/profile/2001",
	}, "\n")
	resp, err := stdhttp.Post(server.URL+"/api/v1/tasks:csv", "text/csv", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/v1/tasks:csv failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	var envelope adminResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("expected error object")
	}
	if envelope.Error.Code != string(AdminErrorCodeQueueWriteFailed) {
		t.Fatalf("expected %q, got %q", AdminErrorCodeQueueWriteFailed, envelope.Error.Code)
	}
}

func newAdminTaskServer(t *testing.T, writer adminCSVQueueWriter) *httptest.Server {
	t.Helper()

	return newAdminTaskServerWithDependencies(t, writer, nil, nil)
}

func newAdminTaskServerWithDependencies(
	t *testing.T,
	writer adminCSVQueueWriter,
	taskReader adminTaskReader,
	resultReader adminResultReader,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(NewServer(
		ServerConfig{Address: ":0"},
		stdhttp.NotFoundHandler(),
		stdhttp.NotFoundHandler(),
		NewAdminTasksHandler(writer, taskReader, resultReader),
	).Handler)
}

type fakeAdminBatchWriter struct {
	result       repository.EnqueueValidatedBatchResult
	err          error
	receivedRows []repository.EnqueueValidatedRow
}

func (f *fakeAdminBatchWriter) EnqueueValidatedBatch(
	ctx context.Context,
	rows []repository.EnqueueValidatedRow,
) (repository.EnqueueValidatedBatchResult, error) {
	_ = ctx
	f.receivedRows = append(f.receivedRows[:0], rows...)
	return f.result, f.err
}

type fakeAdminTaskReader struct {
	task              domain.Task
	err               error
	calls             int
	gotID             uuid.UUID
	retryTask         domain.Task
	retryErr          error
	retryCalls        int
	retrySourceID     uuid.UUID
	cancelTask        domain.Task
	cancelErr         error
	cancelCalls       int
	cancelTaskID      uuid.UUID
	cancelReason      string
	failures          []domain.Task
	listFailuresErr   error
	listFailuresCalls int
	lastLimit         int
	lastOffset        int
}

func (f *fakeAdminTaskReader) GetByID(ctx context.Context, taskID uuid.UUID) (domain.Task, error) {
	_ = ctx
	f.calls++
	f.gotID = taskID
	if f.err != nil {
		return domain.Task{}, f.err
	}
	return f.task, nil
}

func (f *fakeAdminTaskReader) ListFailures(
	ctx context.Context,
	limit int,
	offset int,
) ([]domain.Task, error) {
	_ = ctx
	f.listFailuresCalls++
	f.lastLimit = limit
	f.lastOffset = offset
	if f.listFailuresErr != nil {
		return nil, f.listFailuresErr
	}
	return append([]domain.Task(nil), f.failures...), nil
}

func (f *fakeAdminTaskReader) RetryFromTask(
	ctx context.Context,
	sourceTaskID uuid.UUID,
) (domain.Task, error) {
	_ = ctx
	f.retryCalls++
	f.retrySourceID = sourceTaskID
	if f.retryErr != nil {
		return domain.Task{}, f.retryErr
	}
	return f.retryTask, nil
}

func (f *fakeAdminTaskReader) CancelTask(
	ctx context.Context,
	taskID uuid.UUID,
	reason string,
) (domain.Task, error) {
	_ = ctx
	f.cancelCalls++
	f.cancelTaskID = taskID
	f.cancelReason = reason
	if f.cancelErr != nil {
		return domain.Task{}, f.cancelErr
	}
	return f.cancelTask, nil
}

type fakeAdminResultReader struct {
	result              domain.FollowResult
	err                 error
	calls               int
	gotTaskID           uuid.UUID
	gotAttempt          int
	resultByTaskAttempt map[string]domain.FollowResult
	errByTaskAttempt    map[string]error
}

func (f *fakeAdminResultReader) GetByTaskAttempt(
	ctx context.Context,
	taskID uuid.UUID,
	attempt int,
) (domain.FollowResult, error) {
	_ = ctx
	f.calls++
	f.gotTaskID = taskID
	f.gotAttempt = attempt
	key := taskID.String() + "#" + strconv.Itoa(attempt)
	if f.errByTaskAttempt != nil {
		if mappedErr, ok := f.errByTaskAttempt[key]; ok {
			return domain.FollowResult{}, mappedErr
		}
	}
	if f.resultByTaskAttempt != nil {
		if mappedResult, ok := f.resultByTaskAttempt[key]; ok {
			return mappedResult, nil
		}
		return domain.FollowResult{}, domain.NewDomainError(
			domain.ErrorCodeFollowResultNotFound,
			"follow result not found",
		)
	}
	if f.err != nil {
		return domain.FollowResult{}, f.err
	}
	return f.result, nil
}
