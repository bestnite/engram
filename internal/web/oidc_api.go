package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// This file is the SPA same-origin JSON probe endpoint for the optional OIDC login entry.
//
// The SPA login view cannot know at build time whether the server has OIDC enabled, so a read-only probe endpoint is needed.
// It exposes only "available or not" and the stable start address, and never echoes issuer / client_id / client_secret or any credential.

// registerOIDCRoutes mounts the probe endpoint for the OIDC login entry (a pre-login flow, needing no session).
func (s *Server) registerOIDCRoutes(router *gin.Engine) {
	router.GET("/api/v1/auth/oidc", s.oidcInfo)
}

// oidcInfo returns whether the OIDC login entry is available and the start address (GET /api/v1/auth/oidc).
// enabled is decided from the same source as oidcStart's reachability (oidcLoadConfig + cfg.Usable),
// and start_url matches the OIDC login button's href (/auth/oidc/start), so there is no drift of "the page says A while B is actually called".
func (s *Server) oidcInfo(c *gin.Context) {
	enabled := false
	if cfg, err := s.oidcLoadConfig(c); err == nil && cfg.Usable() {
		enabled = true
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":   enabled,
		"start_url": "/auth/oidc/start",
	})
}
