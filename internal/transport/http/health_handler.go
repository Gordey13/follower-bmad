package httptransport

import (
	"context"
	"encoding/json"
	stdhttp "net/http"

	"follower/internal/observability"
)

type HealthProvider interface {
	Snapshot(ctx context.Context) observability.HealthStatus
}

func NewHealthHandler(provider HealthProvider) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method != stdhttp.MethodGet {
			w.WriteHeader(stdhttp.StatusMethodNotAllowed)
			return
		}

		health := provider.Snapshot(r.Context())
		statusCode := stdhttp.StatusOK
		if health.Status == observability.StatusNotReady {
			statusCode = stdhttp.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(health)
	})
}
