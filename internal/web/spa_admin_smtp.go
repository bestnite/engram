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

// 本文件是管理面板「邮件（SMTP）」页的 SPA JSON 端点（AGENTS.md M1-17）。
//
// 读取与写入复用 mail.NewResolver（环境变量 > settings 表 > 默认值）与 store.PutSecret，
// 与 SSR 页完全同一份解析；口令只回「已配置/未配置」，绝不回显明文。测试连接把服务端的原始
// 错误文本带进响应，供前端显示失败原因。

// adminOutbox 是 outbox 读数。
type adminOutbox struct {
	Pending      int64  `json:"pending"`
	Failed       int64  `json:"failed"`
	LastError    string `json:"last_error"`
	LastAttempts int    `json:"last_attempts"`
}

// adminSMTPResponse 是 SMTP 配置页的读取结果；source 取值 db/env/default。
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

// adminSMTPRequest 是保存与测试共用的请求体；空字段表示沿用已保存值。
type adminSMTPRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	From     string `json:"from"`
	TLSMode  string `json:"tls_mode"`
	Password string `json:"password"`
}

// adminTestResult 是「测试连接」的响应；ok 为 true 时忽略 message。
type adminTestResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// adminSMTP 返回 SMTP 配置与 outbox 读数。
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

// adminSMTPSave 保存 SMTP 配置：非敏感值走 PutSetting，口令走 PutSecret（加密）。
// 校验端口与 TLS 模式，写审计。空字段表示不修改。
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
			// 留空表示不修改，与系统设置页一致。
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
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
			TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "smtp", "via": "spa"},
		})
	}
	c.Status(http.StatusNoContent)
}

// adminSMTPTest 用表单值（缺省回落到已保存值）做一次连接与认证握手，
// 失败时把服务端的原始错误文本带进响应（M1-17 验收点）。
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
	// 表单值覆盖已保存值；留空表示沿用已保存值（口令不回显，只能沿用）。
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
		// 原样带上服务端/网络层的错误文本，而不是只写日志。
		s.logger.Info("spa admin: smtp test connection failed", "host", cfg.Host, "port", cfg.Port, "error", terr)
		result.Code = "failed"
		result.Message = terr.Error()
	} else {
		result.OK = true
	}
	// 「测试连接」会发起管理员指定的出站连接：成功与失败都留痕，只记 host 与结果。
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAdminSMTPTest,
		TargetType: "smtp",
		Detail:     map[string]any{"host": cfg.Host, "ok": result.OK, "via": "spa"},
	})
	c.JSON(http.StatusOK, result)
}
