package observability

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"
)

type AdminInstrumentation struct {
	logger *slog.Logger
}

type AdminOperationSpan struct {
	logger        *slog.Logger
	startedAt     time.Time
	correlationID string
	adminAction   string
	taskID        string
	httpRoute     string
}

func NewAdminInstrumentation(logger *slog.Logger) *AdminInstrumentation {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &AdminInstrumentation{
		logger: logger,
	}
}

func (i *AdminInstrumentation) Start(
	ctx context.Context,
	adminAction string,
	taskID string,
	httpRoute string,
) (context.Context, *AdminOperationSpan) {
	if i == nil {
		i = NewAdminInstrumentation(nil)
	}
	requestContext := AdminRequestContextFrom(ctx)
	requestContext.AdminAction = strings.TrimSpace(adminAction)
	if requestContext.AdminAction == "" {
		requestContext.AdminAction = "admin.unknown"
	}
	requestContext.TaskID = strings.TrimSpace(taskID)
	if requestContext.TaskID == "" {
		requestContext.TaskID = "n/a"
	}
	ctx = WithAdminRequestContext(ctx, requestContext)

	return ctx, &AdminOperationSpan{
		logger:        i.logger,
		startedAt:     time.Now(),
		correlationID: requestContext.CorrelationID,
		adminAction:   requestContext.AdminAction,
		taskID:        requestContext.TaskID,
		httpRoute:     strings.TrimSpace(httpRoute),
	}
}

func (span *AdminOperationSpan) End(
	httpStatusCode int,
	operationResult string,
	errorCode string,
	extras ...any,
) {
	if span == nil || span.logger == nil {
		return
	}

	attrs := AdminLifecycleAttrs(
		AdminLifecycleContext{
			CorrelationID:   span.correlationID,
			AdminAction:     span.adminAction,
			TaskID:          span.taskID,
			OperationResult: operationResult,
			ErrorCode:       errorCode,
			DurationMS:      time.Since(span.startedAt).Milliseconds(),
			HTTPRoute:       span.httpRoute,
			HTTPStatusCode:  httpStatusCode,
		},
		extras...,
	)

	if strings.TrimSpace(strings.ToLower(operationResult)) == "success" {
		span.logger.Info(span.adminAction, attrs...)
		return
	}

	span.logger.Warn(span.adminAction, attrs...)
}
