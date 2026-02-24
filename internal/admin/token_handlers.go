package admin

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/SigmaUno/da-proxy/internal/auth"
)

func (h *handlers) handleListTokens(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	tokens, err := h.deps.TokenStore.List()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list tokens")
	}

	return c.JSON(http.StatusOK, tokens)
}

func (h *handlers) handleGetToken(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token ID")
	}

	token, err := h.deps.TokenStore.Get(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to get token")
	}
	if token == nil {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}

	return c.JSON(http.StatusOK, token)
}

func (h *handlers) handleCreateToken(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	var req auth.CreateTokenRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	if req.Name == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "name is required")
	}

	if req.Scope != "" && !auth.ValidScopes[req.Scope] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid scope: must be read-only, write, or admin")
	}

	result, err := h.deps.TokenStore.Create(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create token")
	}

	return c.JSON(http.StatusCreated, result)
}

func (h *handlers) handleUpdateToken(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token ID")
	}

	var req auth.UpdateTokenRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	if req.Scope != nil && !auth.ValidScopes[*req.Scope] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid scope: must be read-only, write, or admin")
	}

	token, err := h.deps.TokenStore.Update(id, req)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update token")
	}
	if token == nil {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}

	return c.JSON(http.StatusOK, token)
}

func (h *handlers) handleDeleteToken(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token ID")
	}

	if err := h.deps.TokenStore.Delete(id); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}

	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) handleRotateToken(c echo.Context) error {
	if h.deps.TokenStore == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "token store not configured")
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid token ID")
	}

	result, err := h.deps.TokenStore.Rotate(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "token not found")
	}

	return c.JSON(http.StatusOK, result)
}
