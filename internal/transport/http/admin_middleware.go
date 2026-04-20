package httptransport

import (
	"context"
	stdhttp "net/http"
	"regexp"
	"strings"
	"time"

	"follower/internal/audit"
	"follower/internal/observability"

	"github.com/google/uuid"
)

const (
	correlationIDHeader = "X-Correlation-ID"
	adminActorHeader    = "X-Admin-Actor"
)

var correlationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type adminResponseTelemetryCarrier interface {
	setAdminResponseTelemetry(outcome string, errorCode string)
}

type adminTelemetryResponseWriter struct {
	stdhttp.ResponseWriter
	statusCode int
	outcome    string
	errorCode  string
}

func newAdminTelemetryResponseWriter(writer stdhttp.ResponseWriter) *adminTelemetryResponseWriter {
	return &adminTelemetryResponseWriter{
		ResponseWriter: writer,
		statusCode:     0,
		outcome:        "unknown",
		errorCode:      "unknown",
	}
}

func (w *adminTelemetryResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *adminTelemetryResponseWriter) Write(body []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = stdhttp.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (w *adminTelemetryResponseWriter) setAdminResponseTelemetry(outcome string, errorCode string) {
	w.outcome = strings.TrimSpace(strings.ToLower(outcome))
	if w.outcome == "" {
		w.outcome = "unknown"
	}

	normalizedErrorCode := strings.TrimSpace(strings.ToLower(errorCode))
	if normalizedErrorCode == "" {
		normalizedErrorCode = "unknown"
	}
	w.errorCode = normalizedErrorCode
}

func (w *adminTelemetryResponseWriter) statusCodeOrDefault() int {
	if w.statusCode != 0 {
		return w.statusCode
	}
	return stdhttp.StatusOK
}

func (handler adminTasksHandler) wrapWithAdminMiddleware(next stdhttp.Handler) stdhttp.Handler {
	if next == nil {
		return stdhttp.NotFoundHandler()
	}

	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		startedAt := time.Now()
		correlationID := resolveCorrelationID(r.Header.Get(correlationIDHeader))
		w.Header().Set(correlationIDHeader, correlationID)

		requestContext := observability.WithAdminRequestContext(r.Context(), observability.AdminRequestContext{
			CorrelationID: correlationID,
			AdminAction:   buildAdminActionLabel(r.Method, r.URL.Path),
			TaskID:        normalizeAdminTaskIDFromPath(r.PathValue("id")),
		})

		request := r.WithContext(requestContext)
		telemetryWriter := newAdminTelemetryResponseWriter(w)
		next.ServeHTTP(telemetryWriter, request)

		statusCode := telemetryWriter.statusCodeOrDefault()
		route := canonicalAdminRouteLabel(request)
		outcome := telemetryWriter.outcome
		if outcome == "unknown" {
			if statusCode >= 400 {
				outcome = "error"
			} else {
				outcome = "success"
			}
		}
		errorCode := telemetryWriter.errorCode
		if errorCode == "unknown" {
			if statusCode >= 400 {
				errorCode = strings.ToLower(string(AdminErrorCodeInternalAdminAPIError))
			} else {
				errorCode = "none"
			}
		}

		handler.adminMetrics.RecordRequest(
			route,
			request.Method,
			statusCode,
			outcome,
			errorCode,
			time.Since(startedAt),
		)

		if request.Method == stdhttp.MethodGet {
			_, span := handler.instrumenter.Start(request.Context(), buildAdminActionLabel(request.Method, route), normalizeAdminTaskIDFromPath(request.PathValue("id")), route)
			span.End(statusCode, outcome, errorCode)
		}
	})
}

func prepareAdminMutationContext(
	r *stdhttp.Request,
	taskID string,
	adminAction string,
) (context.Context, adminMetaEnvelope) {
	correlationID := resolveCorrelationID(r.Header.Get(correlationIDHeader))
	r.Header.Set(correlationIDHeader, correlationID)

	normalizedTaskID := strings.TrimSpace(taskID)
	if normalizedTaskID == "" {
		normalizedTaskID = "n/a"
	}

	ctx := r.Context()
	ctx = observability.WithAdminRequestContext(ctx, observability.AdminRequestContext{
		CorrelationID: correlationID,
		AdminAction:   strings.TrimSpace(adminAction),
		TaskID:        normalizedTaskID,
	})
	ctx = audit.WithActor(ctx, adminActorFromRequest(r))

	return ctx, adminMetaEnvelope{
		CorrelationID: correlationID,
	}
}

func annotateAdminResponseTelemetry(w stdhttp.ResponseWriter, outcome string, errorCode string) {
	carrier, ok := w.(adminResponseTelemetryCarrier)
	if !ok {
		return
	}
	carrier.setAdminResponseTelemetry(outcome, strings.TrimSpace(strings.ToLower(errorCode)))
}

func resolveCorrelationID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return uuid.NewString()
	}
	if !correlationIDPattern.MatchString(trimmed) {
		return uuid.NewString()
	}
	return trimmed
}

func canonicalAdminRouteLabel(r *stdhttp.Request) string {
	if r == nil {
		return "/api/v1/unknown"
	}

	pattern := strings.TrimSpace(r.Pattern)
	if pattern != "" {
		if idx := strings.Index(pattern, " "); idx >= 0 {
			pattern = strings.TrimSpace(pattern[idx+1:])
		}
	} else {
		pattern = strings.TrimSpace(r.URL.Path)
	}

	if pattern == "" {
		return "/api/v1/unknown"
	}
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	if !strings.HasPrefix(pattern, "/api/v1") {
		pattern = "/api/v1" + pattern
	}
	return pattern
}

func buildAdminActionLabel(method string, route string) string {
	normalizedMethod := strings.TrimSpace(strings.ToLower(method))
	if normalizedMethod == "" {
		normalizedMethod = "unknown"
	}

	normalizedRoute := strings.TrimSpace(strings.ToLower(route))
	if normalizedRoute == "" {
		normalizedRoute = "/api/v1/unknown"
	}
	normalizedRoute = strings.TrimPrefix(normalizedRoute, "/")
	normalizedRoute = strings.ReplaceAll(normalizedRoute, "/", ".")
	normalizedRoute = strings.ReplaceAll(normalizedRoute, "{", "")
	normalizedRoute = strings.ReplaceAll(normalizedRoute, "}", "")
	normalizedRoute = strings.ReplaceAll(normalizedRoute, ":", "")

	return "admin." + normalizedMethod + "." + normalizedRoute
}

func normalizeAdminTaskIDFromPath(pathTaskID string) string {
	trimmed := strings.TrimSpace(pathTaskID)
	if trimmed == "" {
		return "n/a"
	}
	if _, err := uuid.Parse(trimmed); err != nil {
		return "n/a"
	}
	return trimmed
}

func adminActorFromRequest(r *stdhttp.Request) audit.Actor {
	actorID := strings.TrimSpace(r.Header.Get(adminActorHeader))
	if actorID == "" {
		actorID = strings.TrimSpace(r.Header.Get("User-Agent"))
	}
	if actorID == "" {
		actorID = "system"
	}

	return audit.Actor{
		Type: audit.ActorTypeAdminOperator,
		ID:   actorID,
	}
}
