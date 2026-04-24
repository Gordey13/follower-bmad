package config

import (
	"fmt"
	"strings"

	"follower/internal/stackerr"
)

func Validate(cfg Config) error {
	var problems []string

	if cfg.App.Name == "" {
		problems = append(problems, "app.name is required")
	}
	if !contains([]string{"dev", "test", "prod"}, cfg.App.Env) {
		problems = append(problems, "app.env must be one of dev|test|prod")
	}
	if cfg.App.HTTPAddress == "" {
		problems = append(problems, "app.http_address is required")
	}
	if cfg.App.ShutdownTimeoutSeconds <= 0 {
		problems = append(problems, "app.shutdown_timeout_seconds must be > 0")
	}
	if cfg.App.ReadTimeoutSeconds <= 0 {
		problems = append(problems, "app.read_timeout_seconds must be > 0")
	}
	if cfg.App.WriteTimeoutSeconds <= 0 {
		problems = append(problems, "app.write_timeout_seconds must be > 0")
	}
	if cfg.App.IdleTimeoutSeconds <= 0 {
		problems = append(problems, "app.idle_timeout_seconds must be > 0")
	}
	if cfg.Worker.LoopIntervalSeconds <= 0 {
		problems = append(problems, "worker.loop_interval_seconds must be > 0")
	}
	if cfg.Session.AllowMissingPayloadOnFirstRun && !cfg.Session.BootstrapLoginEnabled {
		problems = append(
			problems,
			"session.allow_missing_payload_on_first_run requires session.bootstrap_login_enabled=true",
		)
	}
	if cfg.Postgres.URL == "" {
		problems = append(problems, "postgres.url is required")
	}
	if cfg.MinIO.Endpoint == "" {
		problems = append(problems, "minio.endpoint is required")
	}
	if cfg.MinIO.AccessKey == "" {
		problems = append(problems, "minio.access_key is required")
	}
	if cfg.MinIO.SecretKey == "" {
		problems = append(problems, "minio.secret_key is required")
	}
	if cfg.MinIO.Bucket == "" {
		problems = append(problems, "minio.bucket is required")
	}
	if !contains([]string{"playwright", "mock"}, cfg.Browser.Engine) {
		problems = append(problems, "browser.engine must be playwright|mock")
	}
	if cfg.Browser.LaunchTimeoutSeconds <= 0 {
		problems = append(problems, "browser.launch_timeout_seconds must be > 0")
	}
	if cfg.Browser.Engine == "mock" && cfg.App.Env == "prod" {
		problems = append(problems, "browser.engine mock is not allowed in prod")
	}
	if cfg.Smoke.ArtifactPrefix == "" {
		problems = append(problems, "smoke.artifact_prefix is required")
	}
	if !cfg.Policy.Guardrails.ExcludeWhenLimitReached && !cfg.Policy.Guardrails.RestrictWhenThresholdReached {
		problems = append(problems, "policy.guardrails must enable at least one restriction rule")
	}
	if cfg.Policy.Guardrails.QuarantineOnLimitReached && !cfg.Policy.Guardrails.ExcludeWhenLimitReached {
		problems = append(problems, "policy.guardrails.quarantine_on_limit_reached requires exclude_when_limit_reached=true")
	}

	if len(problems) > 0 {
		return stackerr.New(strings.Join(problems, "; "))
	}

	return nil
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	return fmt.Sprintf("***len:%d", len(secret))
}
