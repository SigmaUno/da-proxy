// Package admin provides the administration API server and handlers.
package admin

import (
	"time"

	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

// Dependencies groups all admin API dependencies.
type Dependencies struct {
	LogBuffer     *logging.RingBuffer
	LogStore      logging.Store
	HealthChecker proxy.HealthChecker
	TokenStore    *auth.SQLiteTokenStore
	Config        *config.Config
	Logger        *zap.Logger
	StartTime     time.Time
	Version       string
}
