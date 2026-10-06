package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// spaProfilePayload 是 SPA 个人资料 API 的公开字段集合。
type spaProfilePayload struct {
	ID            uint64 `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	Locale        string `json:"locale"`
	Timezone      string `json:"timezone"`
	DayCutoffHour *int   `json:"day_cutoff_hour"`
}

type spaProfileRequest struct {
	DisplayName   string `json:"display_name"`
	Locale        string `json:"locale"`
	Timezone      string `json:"timezone"`
	DayCutoffHour *int   `json:"day_cutoff_hour"`
}

type spaLocaleRequest struct {
	Locale string `json:"locale"`
}

func spaUserPayload(u *store.User) spaProfilePayload {
	return spaProfilePayload{
		ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
		Locale: u.Locale, Timezone: u.Timezone, DayCutoffHour: u.DayCutoffHour,
	}
}

// spaProfileSessionOnly 拒绝 bearer 凭据，确保个人资料路由只接受浏览器会话。
func (s *Server) spaProfileSessionOnly(c *gin.Context) (*store.User, bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.GetHeader("Authorization"))), "bearer ") {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
			"code": api.CodeForbidden, "message": api.ErrorMessage(c.Request.Context(), api.CodeForbidden),
		}})
		return nil, false
	}
	if _, ok := api.CurrentAPIKey(c); ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
			"code": api.CodeForbidden, "message": api.ErrorMessage(c.Request.Context(), api.CodeForbidden),
		}})
		return nil, false
	}
	u, ok := auth.CurrentUser(c)
	if !ok || u.Status != store.StatusActive {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": api.CodeUnauthorized, "message": api.ErrorMessage(c.Request.Context(), api.CodeUnauthorized),
		}})
		return nil, false
	}
	return u, true
}

func (s *Server) spaProfileGet(c *gin.Context) {
	u, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	fresh, err := s.users.ByID(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("load SPA profile failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	c.JSON(http.StatusOK, spaUserPayload(fresh))
}

func (s *Server) spaProfilePatch(c *gin.Context) {
	u, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	var req spaProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": api.CodeInvalidRequest, "message": api.ErrorMessage(c.Request.Context(), api.CodeInvalidRequest),
		}})
		return
	}
	fresh, err := s.users.ByID(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("load SPA profile for update failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Locale = strings.TrimSpace(req.Locale)
	req.Timezone = strings.TrimSpace(req.Timezone)
	if req.DisplayName == "" || !s.supportedLocale(req.Locale) || req.Timezone == "" {
		spaProfileBadRequest(c)
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		spaProfileBadRequest(c)
		return
	}
	if req.DayCutoffHour != nil && (*req.DayCutoffHour < 0 || *req.DayCutoffHour > 23) {
		spaProfileBadRequest(c)
		return
	}
	changed := fresh.DisplayName != req.DisplayName || fresh.Locale != req.Locale || fresh.Timezone != req.Timezone || !sameOptionalInt(fresh.DayCutoffHour, req.DayCutoffHour)
	if !changed {
		c.JSON(http.StatusOK, spaUserPayload(fresh))
		return
	}
	fresh.DisplayName = req.DisplayName
	fresh.Locale = req.Locale
	fresh.Timezone = req.Timezone
	fresh.DayCutoffHour = req.DayCutoffHour
	if err := s.users.Update(c.Request.Context(), fresh); err != nil {
		s.logger.Error("update SPA profile failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionUserProfileUpdate, TargetType: "user", TargetID: store.Ptr(u.ID),
		Detail: map[string]any{"locale": req.Locale, "timezone": req.Timezone, "day_cutoff_hour": req.DayCutoffHour, "via": "spa"},
	})
	c.JSON(http.StatusOK, spaUserPayload(fresh))
}

func (s *Server) spaLocalePatch(c *gin.Context) {
	u, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	var req spaLocaleRequest
	if err := c.ShouldBindJSON(&req); err != nil || !s.supportedLocale(strings.TrimSpace(req.Locale)) {
		spaProfileBadRequest(c)
		return
	}
	locale := strings.TrimSpace(req.Locale)
	fresh, err := s.users.ByID(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("load SPA locale update failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	if fresh.Locale != locale {
		fresh.Locale = locale
		if err := s.users.Update(c.Request.Context(), fresh); err != nil {
			s.logger.Error("update SPA locale failed", "user_id", u.ID, "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
				"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
			}})
			return
		}
		s.audit(c.Request.Context(), store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionUserProfileUpdate, TargetType: "user", TargetID: store.Ptr(u.ID),
			Detail: map[string]any{"locale": locale, "via": "spa"},
		})
	}
	c.JSON(http.StatusOK, gin.H{"locale": locale})
}

func spaProfileBadRequest(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"code": api.CodeInvalidRequest, "message": api.ErrorMessage(c.Request.Context(), api.CodeInvalidRequest),
	}})
}

func sameOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
