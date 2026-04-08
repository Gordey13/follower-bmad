package httptransport

import (
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
)

func TestServerExposesOnlyTechnicalRoutes(t *testing.T) {
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
		path       string
		wantStatus int
	}{
		{path: "/healthz", wantStatus: stdhttp.StatusOK},
		{path: "/metrics", wantStatus: stdhttp.StatusOK},
		{path: "/tasks", wantStatus: stdhttp.StatusNotFound},
		{path: "/follow", wantStatus: stdhttp.StatusNotFound},
		{path: "/results", wantStatus: stdhttp.StatusNotFound},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			resp, err := stdhttp.Get(server.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s failed: %v", tc.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d for %s, got %d", tc.wantStatus, tc.path, resp.StatusCode)
			}
		})
	}
}
