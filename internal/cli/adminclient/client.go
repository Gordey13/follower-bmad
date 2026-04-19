package adminclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultHTTPTimeout = 10 * time.Second

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string, httpClient *http.Client) (*Client, error) {
	normalizedURL := strings.TrimSpace(baseURL)
	if normalizedURL == "" {
		return nil, &Error{
			Kind:    ErrorKindValidation,
			Code:    "CLI_CONFIG_ERROR",
			Message: "admin API base URL is required",
		}
	}

	parsedURL, err := url.Parse(normalizedURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, &Error{
			Kind:    ErrorKindValidation,
			Code:    "CLI_CONFIG_ERROR",
			Message: "admin API base URL must be a valid absolute URL",
			Cause:   err,
		}
	}

	normalizedURL = strings.TrimRight(normalizedURL, "/")

	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return &Client{
		baseURL:    normalizedURL,
		httpClient: httpClient,
	}, nil
}

func (c *Client) ListTasks(ctx context.Context) (TaskListResponse, error) {
	var data TaskListResponse
	if err := c.get(ctx, "/api/v1/tasks", &data); err != nil {
		return TaskListResponse{}, err
	}
	return data, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (TaskDetail, error) {
	normalizedID := strings.TrimSpace(taskID)
	if normalizedID == "" {
		return TaskDetail{}, &Error{
			Kind:    ErrorKindValidation,
			Code:    "TASK_ID_INVALID",
			Message: "task id is required",
		}
	}

	var data TaskDetail
	if err := c.get(ctx, "/api/v1/tasks/"+url.PathEscape(normalizedID), &data); err != nil {
		return TaskDetail{}, err
	}
	return data, nil
}

func (c *Client) ListFailures(ctx context.Context) (TaskFailuresResponse, error) {
	var data TaskFailuresResponse
	if err := c.get(ctx, "/api/v1/tasks/failures", &data); err != nil {
		return TaskFailuresResponse{}, err
	}
	return data, nil
}

func (c *Client) get(ctx context.Context, path string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return &Error{
			Kind:    ErrorKindValidation,
			Code:    "CLI_REQUEST_INVALID",
			Message: "failed to prepare admin API request",
			Cause:   err,
		}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{
			Kind:    ErrorKindNetwork,
			Code:    "NETWORK_ERROR",
			Message: "admin API request failed",
			Cause:   err,
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return &Error{
			Kind:       ErrorKindProtocol,
			Code:       "API_MALFORMED_RESPONSE",
			Message:    "admin API returned malformed response",
			StatusCode: resp.StatusCode,
			Cause:      err,
		}
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &Error{
			Kind:       ErrorKindProtocol,
			Code:       "API_MALFORMED_RESPONSE",
			Message:    "admin API returned malformed response",
			StatusCode: resp.StatusCode,
			Cause:      err,
		}
	}

	if envelope.Error != nil {
		code := strings.TrimSpace(envelope.Error.Code)
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = "admin API request failed"
		}
		return &Error{
			Kind:       ErrorKindAPI,
			Code:       code,
			Message:    message,
			StatusCode: resp.StatusCode,
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &Error{
			Kind:       ErrorKindAPI,
			Code:       "HTTP_" + strconv.Itoa(resp.StatusCode),
			Message:    fmt.Sprintf("admin API request failed with status %d", resp.StatusCode),
			StatusCode: resp.StatusCode,
		}
	}

	if len(trimRawJSON(envelope.Data)) == 0 || string(trimRawJSON(envelope.Data)) == "null" {
		return &Error{
			Kind:       ErrorKindProtocol,
			Code:       "API_MALFORMED_RESPONSE",
			Message:    "admin API returned malformed response",
			StatusCode: resp.StatusCode,
		}
	}

	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return &Error{
			Kind:       ErrorKindProtocol,
			Code:       "API_MALFORMED_RESPONSE",
			Message:    "admin API returned malformed response",
			StatusCode: resp.StatusCode,
			Cause:      err,
		}
	}

	return nil
}

type responseEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *apiError       `json:"error"`
	Meta  json.RawMessage `json:"meta"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func trimRawJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}
