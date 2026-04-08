package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAppliesEnvOverrides(t *testing.T) {
	configPath := writeConfigFile(t, `
app:
  name: follower
  env: dev
postgres:
  url: postgres://localhost:5432/db
minio:
  endpoint: localhost:9000
  access_key: user
  secret_key: secret
  use_ssl: false
  bucket: artifacts
proxy:
  enabled: true
browser:
  engine: playwright
  headless: true
smoke:
  artifact_prefix: technical-smoke
`)

	t.Setenv("FOLLOWER_APP_ENV", "test")
	t.Setenv("FOLLOWER_MINIO_USE_SSL", "true")
	t.Setenv("FOLLOWER_BROWSER_ENGINE", "mock")
	t.Setenv("FOLLOWER_PROXY_ENABLED", "false")
	t.Setenv("FOLLOWER_SESSION_BOOTSTRAP_LOGIN_ENABLED", "true")
	t.Setenv("FOLLOWER_SESSION_ALLOW_MISSING_PAYLOAD_ON_FIRST_RUN", "true")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.App.Env != "test" {
		t.Fatalf("expected app env override, got %q", cfg.App.Env)
	}
	if !cfg.MinIO.UseSSL {
		t.Fatalf("expected minio.use_ssl override to be true")
	}
	if cfg.Browser.Engine != "mock" {
		t.Fatalf("expected browser.engine override, got %q", cfg.Browser.Engine)
	}
	if cfg.Proxy.Enabled {
		t.Fatalf("expected proxy.enabled override to be false")
	}
	if !cfg.Session.BootstrapLoginEnabled {
		t.Fatalf("expected session.bootstrap_login_enabled override to be true")
	}
	if !cfg.Session.AllowMissingPayloadOnFirstRun {
		t.Fatalf("expected session.allow_missing_payload_on_first_run override to be true")
	}
}

func TestLoadConfigFailsOnInvalidConfig(t *testing.T) {
	configPath := writeConfigFile(t, `
app:
  name: follower
  env: prod
postgres:
  url: ""
minio:
  endpoint: localhost:9000
  access_key: user
  secret_key: secret
  use_ssl: false
  bucket: artifacts
browser:
  engine: mock
  headless: true
session:
  bootstrap_login_enabled: false
  allow_missing_payload_on_first_run: true
smoke:
  artifact_prefix: technical-smoke
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected Load() to fail for invalid config")
	}

	if !strings.Contains(err.Error(), "postgres.url is required") {
		t.Fatalf("expected postgres.url validation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "browser.engine mock is not allowed in prod") {
		t.Fatalf("expected prod/mock validation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "session.allow_missing_payload_on_first_run requires session.bootstrap_login_enabled=true") {
		t.Fatalf("expected session bootstrap validation error, got %v", err)
	}
}

func TestLoadConfigFailsOnConflictingPolicyGuardrails(t *testing.T) {
	configPath := writeConfigFile(t, `
app:
  name: follower
  env: dev
postgres:
  url: postgres://localhost:5432/db
minio:
  endpoint: localhost:9000
  access_key: user
  secret_key: secret
  use_ssl: false
  bucket: artifacts
browser:
  engine: playwright
  headless: true
smoke:
  artifact_prefix: technical-smoke
policy:
  guardrails:
    exclude_when_limit_reached: false
    restrict_when_threshold_reached: false
    quarantine_on_limit_reached: true
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected Load() to fail for conflicting policy guardrails")
	}

	if !strings.Contains(err.Error(), "policy.guardrails") {
		t.Fatalf("expected policy guardrails validation error, got %v", err)
	}
}

func TestLoadConfigDefaultsProxyEnabledToTrue(t *testing.T) {
	configPath := writeConfigFile(t, `
app:
  name: follower
  env: dev
postgres:
  url: postgres://localhost:5432/db
minio:
  endpoint: localhost:9000
  access_key: user
  secret_key: secret
  use_ssl: false
  bucket: artifacts
browser:
  engine: playwright
  headless: true
smoke:
  artifact_prefix: technical-smoke
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !cfg.Proxy.Enabled {
		t.Fatal("expected proxy.enabled to default to true")
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	return path
}
