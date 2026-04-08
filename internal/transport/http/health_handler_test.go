package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"follower/internal/observability"
)

func TestHealthHandlerReady(t *testing.T) {
	t.Parallel()

	service := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
			{
				Name:     "minio",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
			{
				Name:     "playwright",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		time.Second,
		"1.0.0",
	)

	req := httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHealthHandler(service).ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload observability.HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}

	if payload.Status != observability.StatusReady {
		t.Fatalf("expected ready status, got %q", payload.Status)
	}
}

func TestHealthHandlerNotReadyWhenDependencyFails(t *testing.T) {
	t.Parallel()

	service := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return errors.New("db down")
				}),
			},
			{
				Name:     "minio",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
			{
				Name:     "playwright",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		time.Second,
		"1.0.0",
	)

	req := httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHealthHandler(service).ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var payload observability.HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}

	if payload.Status != observability.StatusNotReady {
		t.Fatalf("expected not-ready status, got %q", payload.Status)
	}

	foundNotReady := false
	for _, dependency := range payload.Dependencies {
		if dependency.Name == "postgres" && dependency.Status == observability.StatusNotReady {
			foundNotReady = true
			break
		}
	}
	if !foundNotReady {
		t.Fatal("expected postgres dependency to be not-ready")
	}
}

func TestHealthHandlerIncludesSafeDiagnostics(t *testing.T) {
	t.Parallel()

	service := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "postgres",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		time.Second,
		"1.0.0",
		map[string]string{
			"audit_trail_source": "postgres.audit_logs",
			"health_endpoint":    "/healthz",
		},
	)

	req := httptest.NewRequest(stdhttp.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	NewHealthHandler(service).ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload observability.HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}

	if payload.Diagnostics["audit_trail_source"] != "postgres.audit_logs" {
		t.Fatalf("expected audit_trail_source diagnostic field, got %+v", payload.Diagnostics)
	}
	if payload.Diagnostics["health_endpoint"] != "/healthz" {
		t.Fatalf("expected health_endpoint diagnostic field, got %+v", payload.Diagnostics)
	}
}
