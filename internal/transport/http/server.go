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

func NewServer(cfg ServerConfig, healthHandler stdhttp.Handler, metricsHandler stdhttp.Handler) *stdhttp.Server {
	mux := stdhttp.NewServeMux()
	mux.Handle("/healthz", healthHandler)
	mux.Handle("/metrics", metricsHandler)

	return &stdhttp.Server{
		Addr:         cfg.Address,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}
