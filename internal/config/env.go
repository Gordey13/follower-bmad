package config

import (
	"fmt"
	"strconv"
	"strings"
)

type envLookup func(string) (string, bool)

func applyEnvOverrides(cfg *Config, lookup envLookup) error {
	if err := applyString(lookup, "FOLLOWER_APP_NAME", &cfg.App.Name); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_APP_ENV", &cfg.App.Env); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_APP_VERSION", &cfg.App.Version); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_APP_HTTP_ADDRESS", &cfg.App.HTTPAddress); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_APP_SHUTDOWN_TIMEOUT_SECONDS", &cfg.App.ShutdownTimeoutSeconds); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_APP_READ_TIMEOUT_SECONDS", &cfg.App.ReadTimeoutSeconds); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_APP_WRITE_TIMEOUT_SECONDS", &cfg.App.WriteTimeoutSeconds); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_APP_IDLE_TIMEOUT_SECONDS", &cfg.App.IdleTimeoutSeconds); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_WORKER_LOOP_INTERVAL_SECONDS", &cfg.Worker.LoopIntervalSeconds); err != nil {
		return err
	}
	if err := applyBool(lookup, "FOLLOWER_SESSION_BOOTSTRAP_LOGIN_ENABLED", &cfg.Session.BootstrapLoginEnabled); err != nil {
		return err
	}
	if err := applyBool(lookup, "FOLLOWER_SESSION_ALLOW_MISSING_PAYLOAD_ON_FIRST_RUN", &cfg.Session.AllowMissingPayloadOnFirstRun); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_POSTGRES_URL", &cfg.Postgres.URL); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_MINIO_ENDPOINT", &cfg.MinIO.Endpoint); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_MINIO_ACCESS_KEY", &cfg.MinIO.AccessKey); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_MINIO_SECRET_KEY", &cfg.MinIO.SecretKey); err != nil {
		return err
	}
	if err := applyBool(lookup, "FOLLOWER_MINIO_USE_SSL", &cfg.MinIO.UseSSL); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_MINIO_BUCKET", &cfg.MinIO.Bucket); err != nil {
		return err
	}
	if err := applyBool(lookup, "FOLLOWER_PROXY_ENABLED", &cfg.Proxy.Enabled); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_BROWSER_ENGINE", &cfg.Browser.Engine); err != nil {
		return err
	}
	if err := applyBool(lookup, "FOLLOWER_BROWSER_HEADLESS", &cfg.Browser.Headless); err != nil {
		return err
	}
	if err := applyInt(lookup, "FOLLOWER_BROWSER_LAUNCH_TIMEOUT_SECONDS", &cfg.Browser.LaunchTimeoutSeconds); err != nil {
		return err
	}
	if err := applyString(lookup, "FOLLOWER_SMOKE_ARTIFACT_PREFIX", &cfg.Smoke.ArtifactPrefix); err != nil {
		return err
	}

	return nil
}

func applyString(lookup envLookup, key string, target *string) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}
	*target = strings.TrimSpace(value)
	return nil
}

func applyInt(lookup envLookup, key string, target *int) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}

	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid integer override for %s", key)
	}

	*target = parsed
	return nil
}

func applyBool(lookup envLookup, key string, target *bool) error {
	value, ok := lookup(key)
	if !ok {
		return nil
	}

	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid bool override for %s", key)
	}

	*target = parsed
	return nil
}
