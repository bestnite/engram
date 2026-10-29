package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "mail (SMTP)" page.
//
// Reads and writes reuse mail.NewResolver (env vars > settings table > defaults) and store.PutSecret,
// with exactly the same parsing as the SSR page; the password only returns "configured/not configured" and never echoes plaintext. The connection test carries the server's raw
// error text into the response so the frontend can show the failure reason.

// adminOutbox is the outbox reading.
type adminOutbox struct {
	Pending      int64  `json:"pending"`
	Failed       int64  `json:"failed"`
	LastError    string `json:"last_error"`
	LastAttempts int    `json:"last_attempts"`
}

// adminSMTPResponse is the read result for the SMTP config page; source is one of db/env/default.
type adminSMTPResponse struct {
	Host               string      `json:"host"`
	HostSource         string      `json:"host_source"`
	Port               string      `json:"port"`
	PortSource         string      `json:"port_source"`
	Username           string      `json:"username"`
	UsernameSource     string      `json:"username_source"`
	From               string      `json:"from"`
	FromSource         string      `json:"from_source"`
	TLSMode            string      `json:"tls_mode"`
	PasswordConfigured bool        `json:"password_configured"`
	PasswordSource     string      `json:"password_source"`
	Configured         bool        `json:"configured"`
	Outbox             adminOutbox `json:"outbox"`
	AdminNotifyReady   bool        `json:"admin_notify_ready"`
}

// adminSMTPRequest is the request body shared by save and test; empty fields mean reuse the saved value.
type adminSMTPRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	From     string `json:"from"`
	TLSMode  string `json:"tls_mode"`
	Password string `json:"password"`
}

// adminTestResult is the "test connection" response; message is ignored when ok is true.
type adminTestResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// adminSMTP returns the SMTP config and the outbox reading.
func (s *Server) adminSMTP(c *gin.Context) {
	ctx := c.Request.Context()
	resolver := mail.NewResolver(s.db, s.secrets)

	host, hostSrc, err := resolver.Field(ctx, mail.SettingKeySMTPHost)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp host failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	port, portSrc, err := resolver.Field(ctx, mail.SettingKeySMTPPort)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp port failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	username, usernameSrc, err := resolver.Field(ctx, mail.SettingKeySMTPUsername)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp username failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	from, fromSrc, err := resolver.Field(ctx, mail.SettingKeySMTPFrom)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp from failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	tlsMode, _, err := resolver.Field(ctx, mail.SettingKeySMTPTLSMode)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp tls mode failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	pwConfigured, pwSrc, err := resolver.PasswordConfigured(ctx)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp password status failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	_, configured, err := resolver.Config(ctx)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	summary, err := store.OutboxSummaryOf(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: read outbox summary failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}

	c.JSON(http.StatusOK, adminSMTPResponse{
		Host: host, HostSource: string(hostSrc),
		Port: port, PortSource: string(portSrc),
		Username: username, UsernameSource: string(usernameSrc),
		From: from, FromSource: string(fromSrc),
		TLSMode:            mail.NormalizeTLSMode(tlsMode),
		PasswordConfigured: pwConfigured, PasswordSource: string(pwSrc),
		Configured: configured,
		Outbox: adminOutbox{
			Pending: summary.Pending, Failed: summary.Failed,
			LastError: summary.LastError, LastAttempts: summary.LastAttempts,
		},
		AdminNotifyReady: configured,
	})
}

// adminSMTPSave saves the SMTP config: non-sensitive values go through PutSetting, the password goes through PutSecret (encrypted).
// Validates the port and TLS mode and writes an audit row. Empty fields mean no change.
func (s *Server) adminSMTPSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminSMTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if raw := strings.TrimSpace(req.Port); raw != "" {
		if n, err := strconv.Atoi(raw); err != nil || n < 1 || n > 65535 {
			adminError(c, http.StatusBadRequest, "invalid_port")
			return
		}
	}
	if raw := strings.TrimSpace(req.TLSMode); raw != "" {
		if !mail.ValidTLSMode(raw) {
			adminError(c, http.StatusBadRequest, "invalid_tls_mode")
			return
		}
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	values := map[string]string{
		mail.SettingKeySMTPHost:     req.Host,
		mail.SettingKeySMTPPort:     req.Port,
		mail.SettingKeySMTPUsername: req.Username,
		mail.SettingKeySMTPFrom:     req.From,
		mail.SettingKeySMTPTLSMode:  req.TLSMode,
	}
	changed := make([]string, 0, 6)
	for _, key := range []string{
		mail.SettingKeySMTPHost, mail.SettingKeySMTPPort, mail.SettingKeySMTPUsername,
		mail.SettingKeySMTPFrom, mail.SettingKeySMTPTLSMode,
	} {
		raw := strings.TrimSpace(values[key])
		if raw == "" {
			// Blank means no change, consistent with the system settings page.
			continue
		}
		if err := store.PutSetting(ctx, s.db, key, raw, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("spa admin: save smtp setting failed", "key", key, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		changed = append(changed, key)
	}
	if s.secrets != nil {
		if pw := req.Password; strings.TrimSpace(pw) != "" {
			if err := store.PutSecret(ctx, s.db, s.secrets, mail.SettingKeySMTPPassword, pw, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("spa admin: save smtp password failed", "error", err)
				adminError(c, http.StatusInternalServerError, "save_failed")
				return
			}
			changed = append(changed, mail.SettingKeySMTPPassword)
		}
	}

	if len(changed) > 0 {
		s.auditor.Record(ctx, store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
			TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "smtp", "via": "spa"},
		})
	}
	c.Status(http.StatusNoContent)
}

// adminSMTPTest performs one connect and auth handshake using the form values (falling back to the saved values when absent),
// and on failure carries the server's raw error text into the response (acceptance point).
func (s *Server) adminSMTPTest(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminSMTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	resolver := mail.NewResolver(s.db, s.secrets)
	cfg, _, err := resolver.Config(ctx)
	if err != nil {
		s.logger.Error("spa admin: resolve smtp config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	// Form values override the saved values; blank means reuse the saved value (the password is never echoed, so it can only be reused).
	if v := strings.TrimSpace(req.Host); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(req.Port); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 1 && n <= 65535 {
			cfg.Port = n
		}
	}
	if v := strings.TrimSpace(req.Username); v != "" {
		cfg.Username = v
	}
	if v := strings.TrimSpace(req.From); v != "" {
		cfg.From = v
	}
	if v := strings.TrimSpace(req.TLSMode); v != "" {
		cfg.TLSMode = mail.NormalizeTLSMode(v)
	}
	if pw := req.Password; strings.TrimSpace(pw) != "" {
		cfg.Password = pw
	}

	result := adminTestResult{}
	if cfg.Host == "" {
		result.Code = "no_host"
	} else if terr := mail.TestConnection(ctx, cfg); terr != nil {
		// Carry the server/network layer's error text through verbatim instead of only logging it.
		s.logger.Info("spa admin: smtp test connection failed", "host", cfg.Host, "port", cfg.Port, "error", terr)
		result.Code = "failed"
		result.Message = terr.Error()
	} else {
		result.OK = true
	}
	// "Test connection" makes an outbound connection to an admin-chosen target: both success and failure leave a trace, recording only the host and the result.
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAdminSMTPTest,
		TargetType: "smtp",
		Detail:     map[string]any{"host": cfg.Host, "ok": result.OK, "via": "spa"},
	})
	c.JSON(http.StatusOK, result)
}
