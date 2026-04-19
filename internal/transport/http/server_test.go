package httptransport

import (
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestServerRegistersTechnicalAndAdminRoutes(t *testing.T) {
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
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "healthz", method: stdhttp.MethodGet, path: "/healthz", wantStatus: stdhttp.StatusOK},
		{name: "metrics", method: stdhttp.MethodGet, path: "/metrics", wantStatus: stdhttp.StatusOK},
		{name: "tasks list skeleton", method: stdhttp.MethodGet, path: "/api/v1/tasks", wantStatus: stdhttp.StatusNotImplemented},
		{name: "task get detail route", method: stdhttp.MethodGet, path: "/api/v1/tasks/00000000-0000-0000-0000-000000000001", wantStatus: stdhttp.StatusServiceUnavailable},
		{name: "task retry skeleton", method: stdhttp.MethodPost, path: "/api/v1/tasks/00000000-0000-0000-0000-000000000001/retry", wantStatus: stdhttp.StatusServiceUnavailable},
		{name: "task cancel skeleton", method: stdhttp.MethodPost, path: "/api/v1/tasks/00000000-0000-0000-0000-000000000001/cancel", wantStatus: stdhttp.StatusServiceUnavailable},
		{name: "task failures route", method: stdhttp.MethodGet, path: "/api/v1/tasks/failures", wantStatus: stdhttp.StatusServiceUnavailable},
		{name: "task csv validation endpoint", method: stdhttp.MethodPost, path: "/api/v1/tasks:csv", wantStatus: stdhttp.StatusBadRequest},
		{name: "legacy tasks remains missing", method: stdhttp.MethodGet, path: "/tasks", wantStatus: stdhttp.StatusNotFound},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req, err := stdhttp.NewRequest(tc.method, server.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("create request %s %s failed: %v", tc.method, tc.path, err)
			}

			resp, err := stdhttp.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s failed: %v", tc.method, tc.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d for %s %s, got %d", tc.wantStatus, tc.method, tc.path, resp.StatusCode)
			}
		})
	}
}
