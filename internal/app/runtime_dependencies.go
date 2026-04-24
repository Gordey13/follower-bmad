package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"follower/internal/audit"
	"follower/internal/browser"
	"follower/internal/config"
	"follower/internal/credentials"
	"follower/internal/domain"
	"follower/internal/observability"
	"follower/internal/repository"
	postgresrepo "follower/internal/repository/postgres"
	"follower/internal/stackerr"
	"follower/internal/storage"
	"follower/internal/worker"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	miniocredentials "github.com/minio/minio-go/v7/pkg/credentials"
)

type runtimeDependencies struct {
	executionService *worker.ExecutionService
	claimLoop        *worker.ClaimLoop
	metricsRefresher *operationalMetricsRefresher
	taskRepository   repository.TaskRepository
	resultRepository repository.ResultRepository
	cleanup          func()
}

func buildRuntimeDependencies(
	cfg config.Config,
	healthService *observability.HealthService,
	taskMetrics *observability.TaskLifecycleMetrics,
	logger *slog.Logger,
) (runtimeDependencies, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.Postgres.URL)
	if err != nil {
		return runtimeDependencies{}, stackerr.Wrap(err, "parse postgres config")
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return runtimeDependencies{}, stackerr.Wrap(err, "create postgres pool")
	}

	minioClient, err := minio.New(cfg.MinIO.Endpoint, &minio.Options{
		Creds:  miniocredentials.NewStaticV4(cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, ""),
		Secure: cfg.MinIO.UseSSL,
	})
	if err != nil {
		pool.Close()
		return runtimeDependencies{}, stackerr.Wrap(err, "create minio client")
	}

	guardrails := domain.RuntimeGuardrails{
		ExcludeWhenLimitReached:      cfg.Policy.Guardrails.ExcludeWhenLimitReached,
		RestrictWhenThresholdReached: cfg.Policy.Guardrails.RestrictWhenThresholdReached,
		QuarantineOnLimitReached:     cfg.Policy.Guardrails.QuarantineOnLimitReached,
		RequireProxyBinding:          cfg.Proxy.Enabled,
	}

	auditRepository := postgresrepo.NewAuditRepository(pool)
	auditLog := audit.NewLog(auditRepository, logger)
	bootstrapCtx := audit.WithActor(context.Background(), audit.Actor{
		Type: audit.ActorTypeInternalProcess,
		ID:   "app.bootstrap",
	})

	recordCtx, cancel := context.WithTimeout(bootstrapCtx, 150*time.Millisecond)
	_, err = auditLog.Record(recordCtx, audit.Event{
		Action:        "config.runtime_guardrails_loaded",
		TargetType:    "config",
		TargetID:      "policy.guardrails",
		ChangeSummary: "runtime guardrails loaded from configuration",
		DiagnosticFields: map[string]string{
			"exclude_when_limit_reached":      strconv.FormatBool(guardrails.ExcludeWhenLimitReached),
			"restrict_when_threshold_reached": strconv.FormatBool(guardrails.RestrictWhenThresholdReached),
			"quarantine_on_limit_reached":     strconv.FormatBool(guardrails.QuarantineOnLimitReached),
			"require_proxy_binding":           strconv.FormatBool(guardrails.RequireProxyBinding),
		},
	})
	cancel()
	if err != nil && logger != nil {
		logger.Warn(
			"bootstrap audit event skipped",
			observability.ErrorLifecycleAttrsWithError(
				observability.LifecycleContext{
					Component:  "app.runtime_dependencies",
					TaskID:     "n/a",
					AccountID:  "n/a",
					Attempt:    0,
					ErrorCode:  appLifecycleErrorCode(err),
					DurationMS: 0,
				},
				err,
				"bootstrap audit event skipped",
				"action", "config.runtime_guardrails_loaded",
			)...,
		)
	}

	recordCtx, cancel = context.WithTimeout(bootstrapCtx, 150*time.Millisecond)
	_, err = auditLog.Record(recordCtx, audit.Event{
		Action:        "readiness.baseline_verified",
		TargetType:    "service",
		TargetID:      cfg.App.Name,
		ChangeSummary: "technical readiness baseline initialized",
		DiagnosticFields: map[string]string{
			"health_endpoint":  "/healthz",
			"metrics_endpoint": "/metrics",
		},
	})
	cancel()
	if err != nil && logger != nil {
		logger.Warn(
			"bootstrap audit event skipped",
			observability.ErrorLifecycleAttrsWithError(
				observability.LifecycleContext{
					Component:  "app.runtime_dependencies",
					TaskID:     "n/a",
					AccountID:  "n/a",
					Attempt:    0,
					ErrorCode:  appLifecycleErrorCode(err),
					DurationMS: 0,
				},
				err,
				"bootstrap audit event skipped",
				"action", "readiness.baseline_verified",
			)...,
		)
	}

	accountRepository := postgresrepo.NewAccountRepository(pool, guardrails, auditLog)
	sessionRepository := postgresrepo.NewSessionRepository(pool, auditLog)
	objectClient := storage.NewMinioSessionObjectClient(minioClient)
	sessionStore := storage.NewSessionStore(objectClient, cfg.MinIO.Bucket)
	screenshotStore := storage.NewScreenshotStore(objectClient, cfg.MinIO.Bucket)
	artifactStore := storage.NewArtifactStore(objectClient, cfg.MinIO.Bucket)
	followRunner, err := browser.NewFollowFlowRunner(cfg.Browser.Engine, logger)
	if err != nil {
		pool.Close()
		return runtimeDependencies{}, stackerr.Wrap(err, "create follow flow runner")
	}
	credentialResolver := credentials.NewResolver()
	bootstrapRunner, err := browser.NewBootstrapLoginRunner(
		cfg.Browser.Engine,
		credentialResolver,
		logger,
	)
	if err != nil {
		pool.Close()
		return runtimeDependencies{}, stackerr.Wrap(err, "create bootstrap login runner")
	}
	verifyRunner, err := browser.NewVerifyFlowRunner(cfg.Browser.Engine, logger)
	if err != nil {
		pool.Close()
		return runtimeDependencies{}, stackerr.Wrap(err, "create verify flow runner")
	}
	taskRepository := postgresrepo.NewTaskRepository(pool, auditLog)
	resultRepository := postgresrepo.NewResultRepository(pool, auditLog)
	sessionRestorer := browser.NewSessionRestorer(sessionRepository, sessionStore, logger)
	accountGuard := worker.NewAccountGuard(accountRepository, guardrails, logger)
	executionService := worker.NewExecutionService(
		accountGuard,
		sessionRestorer,
		logger,
		taskRepository,
	).
		WithSessionBootstrapPolicy(worker.SessionBootstrapPolicy{
			BootstrapLoginEnabled:         cfg.Session.BootstrapLoginEnabled,
			AllowMissingPayloadOnFirstRun: cfg.Session.AllowMissingPayloadOnFirstRun,
		}).
		WithBootstrapLoginRunner(bootstrapRunner).
		WithFollowFlowRunner(followRunner).
		WithVerifyFlowRunner(verifyRunner).
		WithResultRepository(resultRepository).
		WithScreenshotStore(screenshotStore).
		WithArtifactStore(artifactStore)
	claimLoop := worker.NewClaimLoop(
		taskRepository,
		healthService,
		taskMetrics,
		fmt.Sprintf("%s-%d", cfg.App.Name, os.Getpid()),
		time.Duration(cfg.Worker.LoopIntervalSeconds)*time.Second,
		logger,
		executionService,
	)
	metricsRefresher := newOperationalMetricsRefresher(
		taskRepository,
		accountRepository,
		sessionRepository,
		taskMetrics,
		operationalMetricsRefreshInterval(cfg),
		logger,
	)

	return runtimeDependencies{
		executionService: executionService,
		claimLoop:        claimLoop,
		metricsRefresher: metricsRefresher,
		taskRepository:   taskRepository,
		resultRepository: resultRepository,
		cleanup: func() {
			pool.Close()
		},
	}, nil
}
