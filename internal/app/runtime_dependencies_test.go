package app

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"follower/internal/config"
	"follower/internal/observability"
)

func TestBuildRuntimeDependencies(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		App: config.AppConfig{
			Name: "follower",
		},
		Postgres: config.PostgresConfig{
			URL: "postgres://postgres:password@127.0.0.1:5432/follower_automation?sslmode=disable",
		},
		MinIO: config.MinIOConfig{
			Endpoint:  "127.0.0.1:9000",
			AccessKey: "minioadmin",
			SecretKey: "minioadmin",
			UseSSL:    false,
			Bucket:    "artifacts",
		},
		Proxy: config.ProxyConfig{
			Enabled: true,
		},
		Browser: config.BrowserConfig{
			Engine: "mock",
		},
		Session: config.SessionConfig{
			BootstrapLoginEnabled:         true,
			AllowMissingPayloadOnFirstRun: false,
		},
		Policy: config.PolicyConfig{
			Guardrails: config.PolicyGuardrailsConfig{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthService := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "test",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		100,
		"test",
	)
	taskMetrics := observability.NewTaskLifecycleMetrics(observability.NewMetricsRegistry())

	deps, err := buildRuntimeDependencies(cfg, healthService, taskMetrics, logger)
	if err != nil {
		t.Fatalf("buildRuntimeDependencies() error = %v", err)
	}
	if deps.executionService == nil {
		t.Fatal("expected execution service to be initialized")
	}
	if deps.cleanup == nil {
		t.Fatal("expected cleanup function to be initialized")
	}
	if deps.claimLoop == nil {
		t.Fatal("expected claim loop to be initialized")
	}
	if deps.metricsRefresher == nil {
		t.Fatal("expected metrics refresher to be initialized")
	}
	if !executionServiceHasCompleter(deps.executionService) {
		t.Fatal("expected execution service completer to be wired for deterministic preparation completion")
	}
	if !executionServiceHasVerifyRunner(deps.executionService) {
		t.Fatal("expected execution service verify runner to be wired")
	}
	if !executionServiceHasFollowRunner(deps.executionService) {
		t.Fatal("expected execution service follow runner to be wired")
	}
	if !executionServiceHasResultRepository(deps.executionService) {
		t.Fatal("expected execution service result repository to be wired")
	}
	if !executionServiceHasScreenshotStore(deps.executionService) {
		t.Fatal("expected execution service screenshot store to be wired")
	}
	if !executionServiceHasArtifactStore(deps.executionService) {
		t.Fatal("expected execution service artifact store to be wired")
	}
	if !executionServiceHasSessionSaver(deps.executionService) {
		t.Fatal("expected execution service session saver to be wired")
	}
	if !executionServiceHasBootstrapRunner(deps.executionService) {
		t.Fatal("expected execution service bootstrap login runner to be wired")
	}
	if !executionServiceBootstrapPolicy(deps.executionService).BootstrapLoginEnabled {
		t.Fatal("expected execution service bootstrap policy to be wired from config")
	}
	if executionServiceBootstrapPolicy(deps.executionService).AllowMissingPayloadOnFirstRun {
		t.Fatal("expected allow_missing_payload_on_first_run=false when not explicitly configured")
	}

	deps.cleanup()
}

func TestBuildRuntimeDependenciesRejectsInvalidPostgresDSN(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		App: config.AppConfig{
			Name: "follower",
		},
		Postgres: config.PostgresConfig{
			URL: "::invalid-dsn::",
		},
		MinIO: config.MinIOConfig{
			Endpoint:  "127.0.0.1:9000",
			AccessKey: "minioadmin",
			SecretKey: "minioadmin",
			UseSSL:    false,
			Bucket:    "artifacts",
		},
		Proxy: config.ProxyConfig{
			Enabled: true,
		},
		Browser: config.BrowserConfig{
			Engine: "mock",
		},
		Policy: config.PolicyConfig{
			Guardrails: config.PolicyGuardrailsConfig{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthService := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "test",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		100,
		"test",
	)
	taskMetrics := observability.NewTaskLifecycleMetrics(observability.NewMetricsRegistry())

	_, err := buildRuntimeDependencies(cfg, healthService, taskMetrics, logger)
	if err == nil {
		t.Fatal("expected invalid postgres dsn error")
	}
}

func TestBuildRuntimeDependenciesDoesNotFailFastPlaywrightEngine(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		App: config.AppConfig{
			Name: "follower",
		},
		Postgres: config.PostgresConfig{
			URL: "::invalid-dsn::",
		},
		MinIO: config.MinIOConfig{
			Endpoint:  "127.0.0.1:9000",
			AccessKey: "minioadmin",
			SecretKey: "minioadmin",
			UseSSL:    false,
			Bucket:    "artifacts",
		},
		Proxy: config.ProxyConfig{
			Enabled: true,
		},
		Browser: config.BrowserConfig{
			Engine: "playwright",
		},
		Policy: config.PolicyConfig{
			Guardrails: config.PolicyGuardrailsConfig{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	healthService := observability.NewHealthService(
		[]observability.Dependency{
			{
				Name:     "test",
				Critical: true,
				Checker: observability.CheckerFunc(func(ctx context.Context) error {
					return nil
				}),
			},
		},
		100,
		"test",
	)
	taskMetrics := observability.NewTaskLifecycleMetrics(observability.NewMetricsRegistry())

	_, err := buildRuntimeDependencies(cfg, healthService, taskMetrics, logger)
	if err == nil {
		t.Fatal("expected error due to invalid postgres dsn")
	}
	if strings.Contains(err.Error(), "browser.engine=playwright is not supported") {
		t.Fatalf("expected playwright runtime path to be allowed, got fail-fast error: %v", err)
	}
}

func executionServiceHasCompleter(service any) bool {
	return executionServiceHasInterfaceField(service, "completer")
}

func executionServiceHasVerifyRunner(service any) bool {
	return executionServiceHasInterfaceField(service, "verifyRunner")
}

func executionServiceHasFollowRunner(service any) bool {
	return executionServiceHasInterfaceField(service, "followRunner")
}

func executionServiceHasResultRepository(service any) bool {
	return executionServiceHasInterfaceField(service, "resultRepository")
}

func executionServiceHasScreenshotStore(service any) bool {
	return executionServiceHasInterfaceField(service, "screenshotStore")
}

func executionServiceHasArtifactStore(service any) bool {
	return executionServiceHasInterfaceField(service, "artifactStore")
}

func executionServiceHasSessionSaver(service any) bool {
	return executionServiceHasInterfaceField(service, "sessionSaver")
}

func executionServiceHasBootstrapRunner(service any) bool {
	return executionServiceHasInterfaceField(service, "bootstrapRunner")
}

func executionServiceBootstrapPolicy(service any) struct {
	BootstrapLoginEnabled         bool
	AllowMissingPayloadOnFirstRun bool
} {
	empty := struct {
		BootstrapLoginEnabled         bool
		AllowMissingPayloadOnFirstRun bool
	}{}
	if service == nil {
		return empty
	}

	value := reflect.ValueOf(service)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return empty
	}

	field := value.Elem().FieldByName("bootstrapPolicy")
	if !field.IsValid() {
		return empty
	}

	result := empty
	loginEnabled := field.FieldByName("BootstrapLoginEnabled")
	if loginEnabled.IsValid() && loginEnabled.Kind() == reflect.Bool {
		result.BootstrapLoginEnabled = loginEnabled.Bool()
	}
	allowMissingPayload := field.FieldByName("AllowMissingPayloadOnFirstRun")
	if allowMissingPayload.IsValid() && allowMissingPayload.Kind() == reflect.Bool {
		result.AllowMissingPayloadOnFirstRun = allowMissingPayload.Bool()
	}
	return result
}

func executionServiceHasInterfaceField(service any, fieldName string) bool {
	if service == nil {
		return false
	}

	value := reflect.ValueOf(service)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return false
	}

	field := value.Elem().FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.Interface {
		return false
	}

	return !field.IsNil()
}

func TestOperationalMetricsRefreshIntervalIsBounded(t *testing.T) {
	t.Parallel()

	if got := operationalMetricsRefreshInterval(config.Config{
		Worker: config.WorkerConfig{LoopIntervalSeconds: 1},
	}); got != 5*time.Second {
		t.Fatalf("expected lower bound 5s, got %s", got)
	}

	if got := operationalMetricsRefreshInterval(config.Config{
		Worker: config.WorkerConfig{LoopIntervalSeconds: 900},
	}); got != 300*time.Second {
		t.Fatalf("expected upper bound 300s, got %s", got)
	}
}
