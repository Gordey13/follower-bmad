package config

import (
	"bytes"
	"os"
	"strings"

	"follower/internal/stackerr"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config.yaml"

func Load(path string) (Config, error) {
	targetPath := strings.TrimSpace(path)
	if targetPath == "" {
		targetPath = defaultConfigPath
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		return Config{}, stackerr.Wrap(err, "read config file")
	}

	cfg := defaultConfig()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, stackerr.Wrap(err, "parse config yaml")
	}

	if err := applyEnvOverrides(&cfg, os.LookupEnv); err != nil {
		return Config{}, stackerr.WithStack(err)
	}

	if err := Validate(cfg); err != nil {
		return Config{}, stackerr.WithStack(err)
	}

	return cfg, nil
}
