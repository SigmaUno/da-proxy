package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
	return path
}

const fullConfig = `
server:
  listen: ":8443"
  tls_cert: "/path/to/cert.pem"
  tls_key: "/path/to/key.pem"
  read_timeout: 15s
  write_timeout: 20s
  max_body_size: "5MB"

logging:
  level: "debug"
  format: "console"

metrics:
  listen: ":9292"
  enabled: false

backends:
  celestia_app_rpc: "http://10.0.0.1:26657"
  celestia_app_grpc: "10.0.0.1:9090"
  celestia_app_rest: "http://10.0.0.1:1317"
  celestia_node_rpc: "http://10.0.0.1:26658"
  health_check_interval: 10s

tokens:
  - token: "abc123"
    name: "test-token"
    enabled: true
    rate_limit: 100
    allowed_methods: ["blob.Get", "blob.Submit"]
  - token: "def456"
    name: "second-token"
    enabled: false
    rate_limit: 0

admin:
  listen: ":9080"
  username: "testadmin"
  password_hash: "$2a$10$abcdefghijklmnop"
  cors_origins:
    - "http://localhost:3000"
    - "https://admin.example.com"
  log_buffer_size: 50000
  log_retention_days: 14
  log_db_path: "/tmp/test.db"
`

const minimalConfig = `
tokens:
  - token: "abc123"
    name: "test-token"
    enabled: true
`

func TestLoad_FullConfig(t *testing.T) {
	path := writeTestConfig(t, fullConfig)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, ":8443", cfg.Server.Listen)
	assert.Equal(t, "/path/to/cert.pem", cfg.Server.TLSCert)
	assert.Equal(t, "/path/to/key.pem", cfg.Server.TLSKey)
	assert.Equal(t, 15*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, 20*time.Second, cfg.Server.WriteTimeout)
	assert.Equal(t, "5MB", cfg.Server.MaxBodySize)

	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "console", cfg.Logging.Format)

	assert.Equal(t, ":9292", cfg.Metrics.Listen)
	assert.False(t, cfg.Metrics.IsEnabled())

	assert.Equal(t, Endpoints{"http://10.0.0.1:26657"}, cfg.Backends.CelestiaAppRPC)
	assert.Equal(t, Endpoints{"10.0.0.1:9090"}, cfg.Backends.CelestiaAppGRPC)
	assert.Equal(t, Endpoints{"http://10.0.0.1:1317"}, cfg.Backends.CelestiaAppREST)
	assert.Equal(t, Endpoints{"http://10.0.0.1:26658"}, cfg.Backends.CelestiaNodeRPC)
	assert.Equal(t, 10*time.Second, cfg.Backends.HealthCheckInterval)

	require.Len(t, cfg.Tokens, 2)
	assert.Equal(t, "abc123", cfg.Tokens[0].Token)
	assert.Equal(t, "test-token", cfg.Tokens[0].Name)
	assert.True(t, cfg.Tokens[0].Enabled)
	assert.Equal(t, 100, cfg.Tokens[0].RateLimit)
	assert.Equal(t, []string{"blob.Get", "blob.Submit"}, cfg.Tokens[0].AllowedMethods)
	assert.Equal(t, "def456", cfg.Tokens[1].Token)
	assert.False(t, cfg.Tokens[1].Enabled)

	assert.Equal(t, ":9080", cfg.Admin.Listen)
	assert.Equal(t, "testadmin", cfg.Admin.Username)
	assert.Equal(t, "$2a$10$abcdefghijklmnop", cfg.Admin.PasswordHash)
	assert.Equal(t, []string{"http://localhost:3000", "https://admin.example.com"}, cfg.Admin.CORSOrigins)
	assert.Equal(t, 50000, cfg.Admin.LogBufferSize)
	assert.Equal(t, 14, cfg.Admin.LogRetentionDays)
	assert.Equal(t, "/tmp/test.db", cfg.Admin.LogDBPath)
}

func TestLoad_MinimalConfigAppliesDefaults(t *testing.T) {
	path := writeTestConfig(t, minimalConfig)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, ":443", cfg.Server.Listen)
	assert.Equal(t, 30*time.Second, cfg.Server.ReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.Server.WriteTimeout)
	assert.Equal(t, "10MB", cfg.Server.MaxBodySize)

	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)

	assert.Equal(t, ":9191", cfg.Metrics.Listen)
	assert.True(t, cfg.Metrics.IsEnabled())

	assert.Equal(t, Endpoints{"http://127.0.0.1:26657"}, cfg.Backends.CelestiaAppRPC)
	assert.Equal(t, Endpoints{"127.0.0.1:9090"}, cfg.Backends.CelestiaAppGRPC)
	assert.Equal(t, Endpoints{"http://127.0.0.1:1317"}, cfg.Backends.CelestiaAppREST)
	assert.Equal(t, Endpoints{"http://127.0.0.1:26658"}, cfg.Backends.CelestiaNodeRPC)
	assert.Equal(t, 30*time.Second, cfg.Backends.HealthCheckInterval)

	assert.Equal(t, ":8080", cfg.Admin.Listen)
	assert.Equal(t, 100000, cfg.Admin.LogBufferSize)
	assert.Equal(t, 30, cfg.Admin.LogRetentionDays)
	assert.Equal(t, "data/logs.db", cfg.Admin.LogDBPath)
}

func TestLoad_NoTokens(t *testing.T) {
	cfg := `
server:
  listen: ":443"
backends:
  celestia_app_rpc: "http://localhost:26657"
  celestia_node_rpc: "http://localhost:26658"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one token")
}

func TestLoad_TokenMissingName(t *testing.T) {
	cfg := `
tokens:
  - token: "abc123"
    enabled: true
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestLoad_TokenMissingValue(t *testing.T) {
	cfg := `
tokens:
  - name: "test"
    enabled: true
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token value is required")
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	cfg := `
logging:
  level: "trace"
tokens:
  - token: "abc"
    name: "test"
    enabled: true
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logging.level must be one of")
}

func TestLoad_InvalidLogFormat(t *testing.T) {
	cfg := `
logging:
  format: "xml"
tokens:
  - token: "abc"
    name: "test"
    enabled: true
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logging.format must be one of")
}

func TestLoad_InvalidMaxBodySize(t *testing.T) {
	cfg := `
server:
  max_body_size: "notanumber"
tokens:
  - token: "abc"
    name: "test"
    enabled: true
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_body_size")
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTestConfig(t, "{{invalid yaml")
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestMaxBodySizeBytes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{"10MB", "10MB", 10 * 1024 * 1024, false},
		{"5mb lowercase", "5mb", 5 * 1024 * 1024, false},
		{"1GB", "1GB", 1024 * 1024 * 1024, false},
		{"512KB", "512KB", 512 * 1024, false},
		{"100B", "100B", 100, false},
		{"plain number", "1048576", 1048576, false},
		{"empty", "", 0, true},
		{"invalid", "notanumber", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ServerConfig{MaxBodySize: tt.input}
			got, err := s.MaxBodySizeBytes()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveConfigPath(t *testing.T) {
	t.Run("flag takes precedence", func(t *testing.T) {
		t.Setenv("DA_PROXY_CONFIG", "/env/path.yaml")
		assert.Equal(t, "/flag/path.yaml", ResolveConfigPath("/flag/path.yaml"))
	})

	t.Run("env var used when no flag", func(t *testing.T) {
		t.Setenv("DA_PROXY_CONFIG", "/env/path.yaml")
		assert.Equal(t, "/env/path.yaml", ResolveConfigPath(""))
	})

	t.Run("default when nothing set", func(t *testing.T) {
		t.Setenv("DA_PROXY_CONFIG", "")
		assert.Equal(t, "/etc/da-proxy/config.yaml", ResolveConfigPath(""))
	})
}

func TestMetricsConfig_IsEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		m := MetricsConfig{}
		assert.True(t, m.IsEnabled())
	})

	t.Run("explicit true", func(t *testing.T) {
		v := true
		m := MetricsConfig{Enabled: &v}
		assert.True(t, m.IsEnabled())
	})

	t.Run("explicit false", func(t *testing.T) {
		v := false
		m := MetricsConfig{Enabled: &v}
		assert.False(t, m.IsEnabled())
	})
}
