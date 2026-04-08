package app

import (
	"context"
	"fmt"
	"time"

	"follower/internal/config"
	"follower/internal/observability"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/playwright-community/playwright-go"
)

type postgresChecker struct {
	url string
}

func (c postgresChecker) Check(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, c.url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	return conn.Ping(ctx)
}

type minioChecker struct {
	endpoint  string
	accessKey string
	secretKey string
	useSSL    bool
	bucket    string
}

func (c minioChecker) Check(ctx context.Context) error {
	client, err := minio.New(c.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.accessKey, c.secretKey, ""),
		Secure: c.useSSL,
	})
	if err != nil {
		return err
	}

	exists, err := client.BucketExists(ctx, c.bucket)
	return minioBucketHealthError(c.bucket, exists, err)
}

type playwrightChecker struct {
	engine string
}

func (c playwrightChecker) Check(ctx context.Context) error {
	if c.engine == "mock" {
		return nil
	}

	result := make(chan error, 1)
	go func() {
		pw, err := playwright.Run()
		if err != nil {
			result <- err
			return
		}
		defer pw.Stop()
		result <- nil
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}

func buildHealthService(cfg config.Config) *observability.HealthService {
	dependencies := []observability.Dependency{
		{
			Name:     "postgres",
			Critical: true,
			Checker: postgresChecker{
				url: cfg.Postgres.URL,
			},
		},
		{
			Name:     "minio",
			Critical: true,
			Checker: minioChecker{
				endpoint:  cfg.MinIO.Endpoint,
				accessKey: cfg.MinIO.AccessKey,
				secretKey: cfg.MinIO.SecretKey,
				useSSL:    cfg.MinIO.UseSSL,
				bucket:    cfg.MinIO.Bucket,
			},
		},
		{
			Name:     "playwright",
			Critical: true,
			Checker: playwrightChecker{
				engine: cfg.Browser.Engine,
			},
		},
	}

	return observability.NewHealthService(
		dependencies,
		healthCheckTimeout(cfg),
		cfg.App.Version,
		map[string]string{
			"audit_trail_source": "postgres.audit_logs",
			"health_endpoint":    "/healthz",
			"metrics_endpoint":   "/metrics",
		},
	)
}

func minioBucketHealthError(bucket string, exists bool, err error) error {
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("minio bucket %q does not exist", bucket)
	}
	return nil
}

func healthCheckTimeout(cfg config.Config) time.Duration {
	seconds := minPositive(
		cfg.Browser.LaunchTimeoutSeconds,
		cfg.App.ReadTimeoutSeconds,
		cfg.App.WriteTimeoutSeconds,
	)
	if seconds <= 1 {
		return time.Second
	}
	return time.Duration(seconds-1) * time.Second
}

func minPositive(values ...int) int {
	result := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if result == 0 || value < result {
			result = value
		}
	}
	if result == 0 {
		return 1
	}
	return result
}
