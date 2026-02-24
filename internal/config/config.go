// Package config handles loading and validation of da-proxy configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration sections for the da-proxy service.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Backends BackendsConfig `yaml:"backends"`
	Tokens   []TokenConfig  `yaml:"tokens"`
	Admin    AdminConfig    `yaml:"admin"`
	Logging  LoggingConfig  `yaml:"logging"`
	Metrics  MetricsConfig  `yaml:"metrics"`
}

// ServerConfig holds proxy server settings.
type ServerConfig struct {
	Listen       string        `yaml:"listen"`
	TLSCert      string        `yaml:"tls_cert"`
	TLSKey       string        `yaml:"tls_key"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	MaxBodySize  string        `yaml:"max_body_size"`
}

// MaxBodySizeBytes parses the MaxBodySize string into bytes.
func (s ServerConfig) MaxBodySizeBytes() (int64, error) {
	return parseSize(s.MaxBodySize)
}

// BackendsConfig holds backend endpoint URLs.
type BackendsConfig struct {
	CelestiaAppRPC      string        `yaml:"celestia_app_rpc"`
	CelestiaAppGRPC     string        `yaml:"celestia_app_grpc"`
	CelestiaAppREST     string        `yaml:"celestia_app_rest"`
	CelestiaNodeRPC     string        `yaml:"celestia_node_rpc"`
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

// TokenConfig defines a single API token entry.
type TokenConfig struct {
	Token          string   `yaml:"token"`
	Name           string   `yaml:"name"`
	Enabled        bool     `yaml:"enabled"`
	RateLimit      int      `yaml:"rate_limit"`
	AllowedMethods []string `yaml:"allowed_methods"`
}

// AdminConfig holds admin dashboard settings.
type AdminConfig struct {
	Listen           string   `yaml:"listen"`
	Username         string   `yaml:"username"`
	PasswordHash     string   `yaml:"password_hash"`
	CORSOrigins      []string `yaml:"cors_origins"`
	LogBufferSize    int      `yaml:"log_buffer_size"`
	LogRetentionDays int      `yaml:"log_retention_days"`
	LogDBPath        string   `yaml:"log_db_path"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Listen  string `yaml:"listen"`
	Enabled *bool  `yaml:"enabled"`
}

// IsEnabled returns whether metrics collection is enabled.
func (m MetricsConfig) IsEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// Load reads and parses a YAML configuration file from the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	applyDefaults(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Validate checks the configuration for required fields and valid values.
func (c *Config) Validate() error {
	if len(c.Tokens) == 0 {
		return fmt.Errorf("at least one token must be configured")
	}

	for i, t := range c.Tokens {
		if t.Token == "" {
			return fmt.Errorf("token[%d]: token value is required", i)
		}
		if t.Name == "" {
			return fmt.Errorf("token[%d]: name is required", i)
		}
	}

	if c.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}

	if c.Backends.CelestiaAppRPC == "" {
		return fmt.Errorf("backends.celestia_app_rpc is required")
	}

	if c.Backends.CelestiaNodeRPC == "" {
		return fmt.Errorf("backends.celestia_node_rpc is required")
	}

	if _, err := c.Server.MaxBodySizeBytes(); err != nil {
		return fmt.Errorf("server.max_body_size: %w", err)
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("logging.level must be one of: debug, info, warn, error (got %q)", c.Logging.Level)
	}

	validFormats := map[string]bool{"json": true, "console": true}
	if !validFormats[c.Logging.Format] {
		return fmt.Errorf("logging.format must be one of: json, console (got %q)", c.Logging.Format)
	}

	return nil
}

func applyDefaults(c *Config) {
	if c.Server.Listen == "" {
		c.Server.Listen = ":443"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 30 * time.Second
	}
	if c.Server.MaxBodySize == "" {
		c.Server.MaxBodySize = "10MB"
	}

	if c.Backends.CelestiaAppRPC == "" {
		c.Backends.CelestiaAppRPC = "http://127.0.0.1:26657"
	}
	if c.Backends.CelestiaAppGRPC == "" {
		c.Backends.CelestiaAppGRPC = "127.0.0.1:9090"
	}
	if c.Backends.CelestiaAppREST == "" {
		c.Backends.CelestiaAppREST = "http://127.0.0.1:1317"
	}
	if c.Backends.CelestiaNodeRPC == "" {
		c.Backends.CelestiaNodeRPC = "http://127.0.0.1:26658"
	}
	if c.Backends.HealthCheckInterval == 0 {
		c.Backends.HealthCheckInterval = 30 * time.Second
	}

	// Token.Enabled defaults to false from YAML zero value; no override needed currently.

	if c.Admin.Listen == "" {
		c.Admin.Listen = ":8080"
	}
	if c.Admin.LogBufferSize == 0 {
		c.Admin.LogBufferSize = 100000
	}
	if c.Admin.LogRetentionDays == 0 {
		c.Admin.LogRetentionDays = 30
	}
	if c.Admin.LogDBPath == "" {
		c.Admin.LogDBPath = "data/logs.db"
	}

	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}

	if c.Metrics.Listen == "" {
		c.Metrics.Listen = ":9191"
	}
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size string")
	}

	s = strings.ToUpper(s)

	// Check longest suffixes first to avoid "B" matching before "GB"/"MB"/"KB".
	orderedSuffixes := []struct {
		suffix string
		mult   int64
	}{
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}

	for _, entry := range orderedSuffixes {
		if strings.HasSuffix(s, entry.suffix) {
			numStr := strings.TrimSuffix(s, entry.suffix)
			numStr = strings.TrimSpace(numStr)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size number %q: %w", numStr, err)
			}
			return int64(num * float64(entry.mult)), nil
		}
	}

	num, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: must end with B, KB, MB, or GB", s)
	}
	return num, nil
}

// ResolveConfigPath returns the config file path from flag, env, or default.
func ResolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envVal := os.Getenv("DA_PROXY_CONFIG"); envVal != "" {
		return envVal
	}
	return "/etc/da-proxy/config.yaml"
}
