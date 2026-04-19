package httptransport

import (
	stdhttp "net/http"
	"time"
)

type ServerConfig struct {
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

func NewServer(
	cfg ServerConfig,
	healthHandler stdhttp.Handler,
	metricsHandler stdhttp.Handler,
	adminHandler ...stdhttp.Handler,
) *stdhttp.Server {
	adminV1Handler := stdhttp.Handler(NewAdminTasksHandler(nil, nil, nil))
	if len(adminHandler) > 0 && adminHandler[0] != nil {
		adminV1Handler = adminHandler[0]
	}

	mux := stdhttp.NewServeMux()
	mux.Handle("/healthz", healthHandler)
	mux.Handle("/metrics", metricsHandler)
	mux.Handle("/api/v1/", stdhttp.StripPrefix("/api/v1", adminV1Handler))

	return &stdhttp.Server{
		Addr:         cfg.Address,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}
