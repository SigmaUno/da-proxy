package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewLogger_ValidLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}
	for _, lvl := range levels {
		t.Run(lvl, func(t *testing.T) {
			logger, err := NewLogger(lvl, "json")
			require.NoError(t, err)
			assert.NotNil(t, logger)
		})
	}
}

func TestNewLogger_JSONFormat(t *testing.T) {
	logger, err := NewLogger("info", "json")
	require.NoError(t, err)
	assert.NotNil(t, logger)
	// Should not panic when logging
	logger.Info("test message", zap.String("key", "value"))
}

func TestNewLogger_ConsoleFormat(t *testing.T) {
	logger, err := NewLogger("debug", "console")
	require.NoError(t, err)
	assert.NotNil(t, logger)
	logger.Debug("test message", zap.String("key", "value"))
}

func TestNewLogger_InvalidLevel(t *testing.T) {
	_, err := NewLogger("trace", "json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log level")
}

func TestNewLogger_InvalidFormat(t *testing.T) {
	_, err := NewLogger("info", "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid log format")
}

func TestNewLogger_EmptyLevel(t *testing.T) {
	// Empty string unmarshals to info level in Zap, which is valid.
	logger, err := NewLogger("", "json")
	require.NoError(t, err)
	assert.NotNil(t, logger)
}

func TestNewLogger_TypedFields(t *testing.T) {
	logger, err := NewLogger("info", "json")
	require.NoError(t, err)

	// Ensure typed fields work without panic (hot-path logging style)
	logger.Info("request_complete",
		zap.String("request_id", "test-uuid"),
		zap.String("token_name", "my-token"),
		zap.String("method", "blob.Get"),
		zap.String("backend", "celestia-node:26658"),
		zap.Int("status", 200),
		zap.Float64("latency_ms", 42.5),
		zap.Int64("request_bytes", 256),
		zap.Int64("response_bytes", 1024),
		zap.String("client_ip", "127.0.0.1"),
	)
}
