package config

type Config struct {
	App      AppConfig      `yaml:"app"`
	Worker   WorkerConfig   `yaml:"worker"`
	Session  SessionConfig  `yaml:"session"`
	Postgres PostgresConfig `yaml:"postgres"`
	MinIO    MinIOConfig    `yaml:"minio"`
	Proxy    ProxyConfig    `yaml:"proxy"`
	Browser  BrowserConfig  `yaml:"browser"`
	Smoke    SmokeConfig    `yaml:"smoke"`
	Policy   PolicyConfig   `yaml:"policy"`
}

type AppConfig struct {
	Name                   string `yaml:"name"`
	Env                    string `yaml:"env"`
	Version                string `yaml:"version"`
	HTTPAddress            string `yaml:"http_address"`
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds"`
	ReadTimeoutSeconds     int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds    int    `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds     int    `yaml:"idle_timeout_seconds"`
}

type WorkerConfig struct {
	LoopIntervalSeconds int `yaml:"loop_interval_seconds"`
}

type SessionConfig struct {
	BootstrapLoginEnabled         bool `yaml:"bootstrap_login_enabled"`
	AllowMissingPayloadOnFirstRun bool `yaml:"allow_missing_payload_on_first_run"`
}

type PostgresConfig struct {
	URL string `yaml:"url"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	UseSSL    bool   `yaml:"use_ssl"`
	Bucket    string `yaml:"bucket"`
}

type ProxyConfig struct {
	Enabled bool `yaml:"enabled"`
}

type BrowserConfig struct {
	Engine               string `yaml:"engine"`
	Headless             bool   `yaml:"headless"`
	LaunchTimeoutSeconds int    `yaml:"launch_timeout_seconds"`
}

type SmokeConfig struct {
	ArtifactPrefix string `yaml:"artifact_prefix"`
}

type PolicyConfig struct {
	Guardrails PolicyGuardrailsConfig `yaml:"guardrails"`
}

type PolicyGuardrailsConfig struct {
	ExcludeWhenLimitReached      bool `yaml:"exclude_when_limit_reached"`
	RestrictWhenThresholdReached bool `yaml:"restrict_when_threshold_reached"`
	QuarantineOnLimitReached     bool `yaml:"quarantine_on_limit_reached"`
}

func defaultConfig() Config {
	return Config{
		App: AppConfig{
			Env:                    "dev",
			HTTPAddress:            ":8080",
			ShutdownTimeoutSeconds: 20,
			ReadTimeoutSeconds:     10,
			WriteTimeoutSeconds:    10,
			IdleTimeoutSeconds:     60,
		},
		Worker: WorkerConfig{
			LoopIntervalSeconds: 30,
		},
		Session: SessionConfig{
			BootstrapLoginEnabled:         false,
			AllowMissingPayloadOnFirstRun: false,
		},
		Proxy: ProxyConfig{
			Enabled: true,
		},
		Browser: BrowserConfig{
			Headless:             true,
			LaunchTimeoutSeconds: 30,
		},
		Smoke: SmokeConfig{
			ArtifactPrefix: "technical-smoke",
		},
		Policy: PolicyConfig{
			Guardrails: PolicyGuardrailsConfig{
				ExcludeWhenLimitReached:      true,
				RestrictWhenThresholdReached: true,
				QuarantineOnLimitReached:     true,
			},
		},
	}
}
