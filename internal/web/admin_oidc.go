package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「身份与 OIDC」页的 SPA JSON 端点。
//
// 读取与写入复用 oidcLoadConfig 与 s.oidc 客户端，与 SSR 页完全同一份解析；client secret 只回
// 「已配置/未配置」，绝不回显明文。redirect_uri 与登录流程共用 s.oidcRedirectURI，因此页面显示
// 的就是实际发出的值。测试连接把 provider 的原始错误文本带进响应（验收点）。

// adminOIDCIdentity 是一条已绑定身份。
type adminOIDCIdentity struct {
	ID       uint64 `json:"id"`
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	Username string `json:"username"`
	LinkedAt string `json:"linked_at"`
}

// adminOIDCResponse 是 OIDC 配置页的读取结果。
type adminOIDCResponse struct {
	Enabled            bool                `json:"enabled"`
	Issuer             string              `json:"issuer"`
	ClientID           string              `json:"client_id"`
	SecretConfigured   bool                `json:"secret_configured"`
	RedirectURI        string              `json:"redirect_uri"`
	Scopes             string              `json:"scopes"`
	ClaimSubject       string              `json:"claim_subject"`
	ClaimEmail         string              `json:"claim_email"`
	ClaimName          string              `json:"claim_name"`
	ClaimEmailVerified string              `json:"claim_email_verified"`
	Identities         []adminOIDCIdentity `json:"identities"`
}

// adminOIDCRequest 是保存 / 测试共用的请求体；空字段表示不修改。
type adminOIDCRequest struct {
	Enabled            bool   `json:"enabled"`
	Issuer             string `json:"issuer"`
	ClientID           string `json:"client_id"`
	Scopes             string `json:"scopes"`
	ClaimSubject       string `json:"claim_subject"`
	ClaimEmail         string `json:"claim_email"`
	ClaimName          string `json:"claim_name"`
	ClaimEmailVerified string `json:"claim_email_verified"`
	ClientSecret       string `json:"client_secret"`
}

// adminOIDC 返回 OIDC 配置、回调地址与已绑定身份列表。
func (s *Server) adminOIDC(c *gin.Context) {
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	saved, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: load settings failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	secretConfigured := false
	if s.db != nil {
		if configured, err := store.SecretConfigured(ctx, s.db, auth.SettingKeyOIDCClientSecret); err == nil && configured {
			secretConfigured = true
		}
	}

	resp := adminOIDCResponse{
		Enabled:            cfg.Enabled,
		Issuer:             saved[auth.SettingKeyOIDCIssuer],
		ClientID:           saved[auth.SettingKeyOIDCClientID],
		SecretConfigured:   secretConfigured,
		RedirectURI:        s.oidcRedirectURI(c),
		Scopes:             saved[auth.SettingKeyOIDCScopes],
		ClaimSubject:       saved[auth.SettingKeyOIDCClaimSubject],
		ClaimEmail:         saved[auth.SettingKeyOIDCClaimEmail],
		ClaimName:          saved[auth.SettingKeyOIDCClaimName],
		ClaimEmailVerified: saved[auth.SettingKeyOIDCClaimEmailVerified],
		Identities:         []adminOIDCIdentity{},
	}
	if s.identities != nil {
		rows, err := s.identities.ListAll(ctx)
		if err != nil {
			s.logger.Error("spa admin: list identities failed", "error", err)
			adminError(c, http.StatusInternalServerError, "internal_error")
			return
		}
		for _, row := range rows {
			username := ""
			if s.users != nil {
				if u, err := s.users.ByID(ctx, row.UserID); err == nil && u != nil {
					username = u.Username
				}
			}
			email := ""
			if row.Email != nil {
				email = *row.Email
			}
			resp.Identities = append(resp.Identities, adminOIDCIdentity{
				ID: row.ID, Provider: row.Provider, Subject: row.Subject,
				Email: email, Username: username,
				LinkedAt: row.LinkedAt.Format(time.RFC3339),
			})
		}
	}
	c.JSON(http.StatusOK, resp)
}

// adminOIDCSave 保存 OIDC 配置：非敏感值走 PutSetting，client secret 走 PutSecret（加密）。
// 保存后使发现文档缓存失效，写审计。
func (s *Server) adminOIDCSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminOIDCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(req.Issuer), "/")
	if req.Enabled && issuer != "" {
		if parsed, err := url.Parse(issuer); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			adminError(c, http.StatusBadRequest, "invalid_issuer")
			return
		}
	}
	if req.Enabled && issuer == "" {
		adminError(c, http.StatusBadRequest, "invalid_issuer")
		return
	}

	ctx := c.Request.Context()
	// 先记录旧 issuer 以便失效缓存。
	old, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}

	now := time.Now().UTC()
	enabledValue := "false"
	if req.Enabled {
		enabledValue = "true"
	}
	writes := []struct{ key, value string }{
		{auth.SettingKeyOIDCEnabled, enabledValue},
	}
	for _, kv := range []struct{ key, value string }{
		{auth.SettingKeyOIDCIssuer, issuer},
		{auth.SettingKeyOIDCClientID, strings.TrimSpace(req.ClientID)},
		{auth.SettingKeyOIDCScopes, strings.TrimSpace(req.Scopes)},
		{auth.SettingKeyOIDCClaimSubject, strings.TrimSpace(req.ClaimSubject)},
		{auth.SettingKeyOIDCClaimEmail, strings.TrimSpace(req.ClaimEmail)},
		{auth.SettingKeyOIDCClaimName, strings.TrimSpace(req.ClaimName)},
		{auth.SettingKeyOIDCClaimEmailVerified, strings.TrimSpace(req.ClaimEmailVerified)},
	} {
		if kv.value != "" {
			writes = append(writes, kv)
		}
	}
	changed := make([]string, 0, len(writes)+1)
	for _, w := range writes {
		if err := store.PutSetting(ctx, s.db, w.key, w.value, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("spa admin: save oidc setting failed", "key", w.key, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		changed = append(changed, w.key)
	}
	if s.secrets != nil {
		if secret := req.ClientSecret; strings.TrimSpace(secret) != "" {
			if err := store.PutSecret(ctx, s.db, s.secrets, auth.SettingKeyOIDCClientSecret, secret, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("spa admin: save oidc client secret failed", "error", err)
				adminError(c, http.StatusInternalServerError, "save_failed")
				return
			}
			changed = append(changed, auth.SettingKeyOIDCClientSecret)
		}
	}

	// 配置变更后失效发现文档缓存：旧 issuer 与新 issuer 都清一遍。
	s.oidc.Invalidate(old.Issuer)
	s.oidc.Invalidate(issuer)
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionSettingUpdate,
		TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "oidc", "via": "spa"},
	})
	c.Status(http.StatusNoContent)
}

// adminOIDCTest 对表单里的 issuer（缺省用已保存值）拉取发现文档，
// 失败时把 provider 的原始错误文本带进响应（验收点）。
func (s *Server) adminOIDCTest(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminOIDCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("spa admin: load oidc config failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(req.Issuer), "/")
	if issuer == "" {
		issuer = cfg.Issuer
	}
	result := adminTestResult{}
	if issuer == "" {
		result.Code = "no_issuer"
	} else if _, derr := s.oidc.Discover(ctx, issuer); derr != nil {
		// 原样带上 provider 的错误文本，而不是只写日志。
		s.logger.Info("spa admin: oidc test connection failed", "issuer", issuer, "error", derr)
		result.Code = "failed"
		result.Message = derr.Error()
	} else {
		result.OK = true
	}
	// 「测试连接」会发起管理员指定的出站连接：成功与失败都留痕，只记 issuer 与结果。
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAdminOIDCTest,
		TargetType: "oidc",
		Detail:     map[string]any{"issuer": issuer, "ok": result.OK, "via": "spa"},
	})
	c.JSON(http.StatusOK, result)
}

// adminOIDCUnlink 解绑一条外部身份并写审计。解绑后该身份不能再用它登录。
func (s *Server) adminOIDCUnlink(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		adminError(c, http.StatusNotFound, "unlink_failed")
		return
	}
	ctx := c.Request.Context()
	ident, err := s.identities.ByID(ctx, id)
	if err != nil {
		s.logger.Error("spa admin: load identity failed", "id", id, "error", err)
		adminError(c, http.StatusNotFound, "unlink_failed")
		return
	}
	if err := s.identities.Delete(ctx, id); err != nil {
		s.logger.Error("spa admin: unlink identity failed", "id", id, "error", err)
		adminError(c, http.StatusInternalServerError, "unlink_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionIdentityUnlink,
		TargetType: "identity", TargetID: store.Ptr(id),
		Detail: map[string]any{"provider": ident.Provider, "subject": ident.Subject, "user_id": ident.UserID, "via": "spa"},
	})
	c.Status(http.StatusNoContent)
}
