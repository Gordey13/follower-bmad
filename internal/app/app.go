package app

import (
	"context"
	"errors"
	"log/slog"
	stdhttp "net/http"
	"time"

	"follower/internal/config"
	"follower/internal/observability"
	httptransport "follower/internal/transport/http"
	"follower/internal/worker"
)

type App struct {
	server           *stdhttp.Server
	shutdownTimeout  time.Duration
	executionService *worker.ExecutionService
	claimLoop        *worker.ClaimLoop
	metricsRefresher *operationalMetricsRefresher
	cleanup          func()
	logger           *slog.Logger
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	metricsRegistry := observability.NewMetricsRegistry()
	taskMetrics := observability.NewTaskLifecycleMetrics(metricsRegistry)
	healthService := buildHealthService(cfg)
	healthService.SetDependencyObserver(taskMetrics)
	runtimeDeps, err := buildRuntimeDependencies(cfg, healthService, taskMetrics, logger)
	if err != nil {
		return nil, err
	}

	server := httptransport.NewServer(
		httptransport.ServerConfig{
			Address:      cfg.App.HTTPAddress,
			ReadTimeout:  time.Duration(cfg.App.ReadTimeoutSeconds) * time.Second,
			WriteTimeout: time.Duration(cfg.App.WriteTimeoutSeconds) * time.Second,
			IdleTimeout:  time.Duration(cfg.App.IdleTimeoutSeconds) * time.Second,
		},
		httptransport.NewHealthHandler(healthService),
		httptransport.NewMetricsHandler(observability.NewMetricsHandler(metricsRegistry)),
		httptransport.NewAdminTasksHandler(
			runtimeDeps.taskRepository,
			runtimeDeps.taskRepository,
			runtimeDeps.resultRepository,
		),
	)

	return &App{
		server:           server,
		shutdownTimeout:  time.Duration(cfg.App.ShutdownTimeoutSeconds) * time.Second,
		executionService: runtimeDeps.executionService,
		claimLoop:        runtimeDeps.claimLoop,
		metricsRefresher: runtimeDeps.metricsRefresher,
		cleanup:          runtimeDeps.cleanup,
		logger:           logger,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	if a.cleanup != nil {
		defer a.cleanup()
	}

	a.logger.Info("http server starting", "address", a.server.Addr)
	if a.executionService != nil {
		a.logger.Info("worker execution service initialized", "component", "worker.execution_service")
	}
	if a.metricsRefresher != nil {
		a.logger.Info("operational metrics refresher initialized", "component", "observability.operational_metrics")
		go a.metricsRefresher.Run(ctx)
	}
	if a.claimLoop != nil {
		a.logger.Info("worker claim loop initialized", "component", "worker.claim_loop")
		go a.claimLoop.Run(ctx)
	}

	serverErr := make(chan error, 1)
	go func() {
		err := a.server.ListenAndServe()
		if err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
		defer cancel()

		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return err
		}

		return nil
	}
}
