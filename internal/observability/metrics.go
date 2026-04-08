package observability

import (
	stdhttp "net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewMetricsRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector())
	return registry
}

func NewMetricsHandler(registry *prometheus.Registry) stdhttp.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
