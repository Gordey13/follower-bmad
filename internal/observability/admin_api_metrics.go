package observability

import (
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type AdminAPIMetrics struct {
	requestsTotal   *prometheus.CounterVec
	errorsTotal     *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func NewAdminAPIMetrics(registry *prometheus.Registry) *AdminAPIMetrics {
	metrics := &AdminAPIMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_admin_api_requests_total",
			Help: "Total number of admin API requests partitioned by normalized labels.",
		}, []string{"route", "method", "status_class", "outcome", "error_code"}),
		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "follower_admin_api_errors_total",
			Help: "Total number of admin API requests завершившихся ошибкой.",
		}, []string{"route", "method", "status_class", "outcome", "error_code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "follower_admin_api_request_duration_seconds",
			Help:    "Latency histogram for admin API requests in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status_class", "outcome", "error_code"}),
	}

	if registry != nil {
		registry.MustRegister(metrics.requestsTotal, metrics.errorsTotal, metrics.requestDuration)
	}

	return metrics
}

func (m *AdminAPIMetrics) RecordRequest(
	route string,
	method string,
	statusCode int,
	outcome string,
	errorCode string,
	duration time.Duration,
) {
	if m == nil {
		return
	}

	routeLabel := normalizeAdminRouteLabel(route)
	methodLabel := normalizeAdminMethodLabel(method)
	statusClassLabel := normalizeAdminStatusClassLabel(statusCode)
	outcomeLabel := normalizeAdminOutcomeLabel(outcome)
	errorCodeLabel := normalizeAdminErrorCodeLabel(errorCode)

	labels := []string{routeLabel, methodLabel, statusClassLabel, outcomeLabel, errorCodeLabel}
	m.requestsTotal.WithLabelValues(labels...).Inc()
	m.requestDuration.WithLabelValues(labels...).Observe(duration.Seconds())
	if outcomeLabel == "error" || strings.HasPrefix(statusClassLabel, "5") || strings.HasPrefix(statusClassLabel, "4") {
		m.errorsTotal.WithLabelValues(labels...).Inc()
	}
}

var allowedAdminRoutes = setOf(
	"/api/v1/tasks",
	"/api/v1/tasks/{id}",
	"/api/v1/tasks/{id}/retry",
	"/api/v1/tasks/{id}/cancel",
	"/api/v1/tasks/failures",
	"/api/v1/tasks:csv",
	"/api/v1/unknown",
)

var allowedAdminMethods = setOf(
	"get",
	"post",
	"put",
	"patch",
	"delete",
	"head",
	"options",
	"unknown",
)

var allowedAdminStatusClasses = setOf(
	"1xx",
	"2xx",
	"3xx",
	"4xx",
	"5xx",
	"unknown",
)

var allowedAdminOutcomes = setOf(
	"success",
	"error",
	"rejected",
	"unknown",
)

var allowedAdminErrorCodes = setOf(
	"task_not_found",
	"task_id_invalid",
	"task_state_conflict",
	"retry_not_allowed",
	"cancel_not_allowed",
	"internal_admin_api_error",
	"csv_schema_invalid",
	"csv_row_invalid",
	"queue_write_failed",
	"admin_endpoint_not_implemented",
	"admin_endpoint_not_available",
	"none",
	"unknown",
)

func normalizeAdminRouteLabel(route string) string {
	trimmed := strings.TrimSpace(route)
	if trimmed == "" {
		return "/api/v1/unknown"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	if !strings.HasPrefix(trimmed, "/api/v1") {
		trimmed = "/api/v1" + trimmed
	}
	return normalizeBoundedLabel(trimmed, allowedAdminRoutes, "/api/v1/unknown")
}

func normalizeAdminMethodLabel(method string) string {
	return normalizeBoundedLabel(method, allowedAdminMethods, "unknown")
}

func normalizeAdminStatusClassLabel(statusCode int) string {
	if statusCode < 100 || statusCode > 599 {
		return "unknown"
	}
	class := strconv.Itoa(statusCode/100) + "xx"
	return normalizeBoundedLabel(class, allowedAdminStatusClasses, "unknown")
}

func normalizeAdminOutcomeLabel(outcome string) string {
	return normalizeBoundedLabel(outcome, allowedAdminOutcomes, "unknown")
}

func normalizeAdminErrorCodeLabel(code string) string {
	return normalizeBoundedLabel(code, allowedAdminErrorCodes, "unknown")
}
