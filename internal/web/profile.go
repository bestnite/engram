package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// profilePayload 是 SPA 个人资料 API 的公开字段集合。
type profilePayload struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	Locale        string `json:"locale"`
	Timezone      string `json:"timezone"`
	DayCutoffHour *int   `json:"day_cutoff_hour"`
	// LearnAheadMinutes 是提前学习窗口（分钟）；null 表示用默认值。
	LearnAheadMinutes *int `json:"learn_ahead_minutes"`
}

// profileRequest 是个人资料的完整表单；learn_ahead_minutes 缺省或 null 表示回到默认值。
type profileRequest struct {
	DisplayName       string `json:"display_name"`
	Locale            string `json:"locale"`
	Timezone          string `json:"timezone"`
	DayCutoffHour     *int   `json:"day_cutoff_hour"`
	LearnAheadMinutes *int   `json:"learn_ahead_minutes"`
}

type localeRequest struct {
	Locale string `json:"locale"`
}

type passwordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) passwordPatch(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req passwordRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		passwordError(c, http.StatusBadRequest, "invalid_request", "The password request is invalid.")
		return
	}
	fresh, err := s.users.ByID(c.Request.Context(), u.ID)
	if err != nil {
		s.logger.Error("load user for SPA password change failed", "user_id", u.ID, "error", err)
		passwordError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	if fresh.PasswordHash == nil {
		passwordError(c, http.StatusConflict, "password_unavailable", "Password change is unavailable for this account.")
		return
	}
	sess, _ := auth.CurrentSession(c)
	keep := ""
	if sess != nil {
		keep = sess.ID
	}
	err = s.accounts.ChangePasswordKeepingSession(c.Request.Context(), u.ID, keep, req.OldPassword, req.NewPassword)
	if err != nil {
		code, status, message := "password_rejected", http.StatusBadRequest, "The new password does not meet the password policy."
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			code, status, message = "invalid_current_password", http.StatusUnauthorized, "The current password is incorrect."
		case errors.Is(err, auth.ErrPasswordUnchanged):
			code, status, message = "password_unchanged", http.StatusBadRequest, "The new password must differ from the current password."
		}
		passwordError(c, status, code, message)
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{UserID: store.Ptr(u.ID), Action: store.ActionUserPasswordChange, TargetType: "user", TargetID: store.Ptr(u.ID)})
	s.notifyCredentialChanged(c.Request.Context(), fresh, "password")
	c.Status(http.StatusNoContent)
}

func passwordError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func userPayload(u *store.User) profilePayload {
	return profilePayload{
		ID: u.PublicID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
		Locale: u.Locale, Timezone: u.Timezone, DayCutoffHour: u.DayCutoffHour,
		LearnAheadMinutes: u.LearnAheadMinutes,
	}
}

// profileSessionOnly 拒绝 bearer 凭据，确保个人资料路由只接受浏览器会话。
func (s *Server) profileSessionOnly(c *gin.Context) (*store.User, bool) {
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

func (s *Server) profileGet(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
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
	c.JSON(http.StatusOK, userPayload(fresh))
}

func (s *Server) profilePatch(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req profileRequest
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
		profileBadRequest(c)
		return
	}
	if _, err := time.LoadLocation(req.Timezone); err != nil {
		profileBadRequest(c)
		return
	}
	if req.DayCutoffHour != nil && (*req.DayCutoffHour < 0 || *req.DayCutoffHour > 23) {
		profileBadRequest(c)
		return
	}
	if req.LearnAheadMinutes != nil && (*req.LearnAheadMinutes < 0 || *req.LearnAheadMinutes > store.MaxLearnAheadMinutes) {
		profileBadRequest(c)
		return
	}
	changed := fresh.DisplayName != req.DisplayName || fresh.Locale != req.Locale || fresh.Timezone != req.Timezone ||
		!sameOptionalInt(fresh.DayCutoffHour, req.DayCutoffHour) || !sameOptionalInt(fresh.LearnAheadMinutes, req.LearnAheadMinutes)
	if !changed {
		c.JSON(http.StatusOK, userPayload(fresh))
		return
	}
	fresh.DisplayName = req.DisplayName
	fresh.Locale = req.Locale
	fresh.Timezone = req.Timezone
	fresh.DayCutoffHour = req.DayCutoffHour
	fresh.LearnAheadMinutes = req.LearnAheadMinutes
	if err := s.users.Update(c.Request.Context(), fresh); err != nil {
		s.logger.Error("update SPA profile failed", "user_id", u.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionUserProfileUpdate, TargetType: "user", TargetID: store.Ptr(u.ID),
		Detail: map[string]any{"locale": req.Locale, "timezone": req.Timezone, "day_cutoff_hour": req.DayCutoffHour,
			"learn_ahead_minutes": req.LearnAheadMinutes, "via": "spa"},
	})
	c.JSON(http.StatusOK, userPayload(fresh))
}

func (s *Server) localePatch(c *gin.Context) {
	u, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	var req localeRequest
	if err := c.ShouldBindJSON(&req); err != nil || !s.supportedLocale(strings.TrimSpace(req.Locale)) {
		profileBadRequest(c)
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

func profileBadRequest(c *gin.Context) {
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
