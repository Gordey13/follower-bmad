package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"follower/internal/config"
)

func TestMinioBucketHealthError(t *testing.T) {
	t.Parallel()

	err := minioBucketHealthError("artifacts", false, nil)
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing bucket message, got %v", err)
	}
}

func TestMinioBucketHealthErrorPassthrough(t *testing.T) {
	t.Parallel()

	upstreamErr := errors.New("network error")
	err := minioBucketHealthError("artifacts", false, upstreamErr)
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("expected upstream error passthrough, got %v", err)
	}
}

func TestHealthCheckTimeoutUsesHTTPBudget(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		App: config.AppConfig{
			ReadTimeoutSeconds:  10,
			WriteTimeoutSeconds: 8,
		},
		Browser: config.BrowserConfig{
			LaunchTimeoutSeconds: 30,
		},
	}

	timeout := healthCheckTimeout(cfg)
	if timeout != 7*time.Second {
		t.Fatalf("expected 7s timeout, got %s", timeout)
	}
}

func TestBuildHealthServiceDiagnosticsDoNotExposeSecrets(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		App: config.AppConfig{
			Version: "0.1.0",
		},
		Postgres: config.PostgresConfig{
			URL: "postgres://postgres:password@127.0.0.1:5432/follower_automation?sslmode=disable",
		},
		MinIO: config.MinIOConfig{
			Endpoint:  "127.0.0.1:9000",
			AccessKey: "minioadmin",
			SecretKey: "minio-secret-value",
			UseSSL:    false,
			Bucket:    "artifacts",
		},
		Browser: config.BrowserConfig{
			Engine:               "mock",
			LaunchTimeoutSeconds: 30,
		},
	}

	snapshot := buildHealthService(cfg).Snapshot(context.Background())
	serialized := strings.ToLower(snapshot.Diagnostics["audit_trail_source"] + snapshot.Diagnostics["health_endpoint"] + snapshot.Diagnostics["metrics_endpoint"])
	if strings.Contains(serialized, "secret") || strings.Contains(serialized, "password") || strings.Contains(serialized, "minioadmin") {
		t.Fatalf("health diagnostics must not expose sensitive values, got %+v", snapshot.Diagnostics)
	}
}
