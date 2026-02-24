package admin

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// Server is the admin API server.
type Server struct {
	echo       *echo.Echo
	listenAddr string
	logger     *zap.Logger
}

// NewServer creates the admin API server.
func NewServer(cfg config.AdminConfig, deps Dependencies) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Recovery middleware.
	e.Use(echomw.Recover())

	// CORS.
	if len(cfg.CORSOrigins) > 0 {
		e.Use(echomw.CORSWithConfig(echomw.CORSConfig{
			AllowOrigins: cfg.CORSOrigins,
			AllowMethods: []string{http.MethodGet, http.MethodOptions},
			AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAuthorization},
		}))
	}

	// Basic Auth.
	if cfg.Username != "" && cfg.PasswordHash != "" {
		e.Use(echomw.BasicAuth(func(username, password string, _ echo.Context) (bool, error) {
			if username != cfg.Username {
				return false, nil
			}
			err := bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(password))
			return err == nil, nil
		}))
	}

	s := &Server{
		echo:       e,
		listenAddr: cfg.Listen,
		logger:     deps.Logger,
	}

	h := &handlers{deps: deps}
	registerRoutes(e, h)

	return s
}

// Start begins serving the admin API.
func (s *Server) Start() error {
	s.logger.Info("admin server starting", zap.String("addr", s.listenAddr))
	return s.echo.Start(s.listenAddr)
}

// Shutdown gracefully shuts down the admin server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}
