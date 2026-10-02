package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// OIDC 配置页与已绑定身份管理（DESIGN.md §8.4；ROADMAP.md M6-4）。
//
// 页面展示：启用开关、issuer、client id、client secret（只显示已配置/未配置）、claim 映射、
// 「测试连接」按钮（把 provider 的错误文本显示在页面上），以及已绑定身份列表与解绑。
// 所有写操作过 CSRF、写审计；用户可见文案全部来自语言包。

// adminOIDCPage 渲染 OIDC 配置页。
func (s *Server) adminOIDCPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("admin: load oidc config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data, err := s.oidcAdminData(c, loc, cfg, "", false)
	if err != nil {
		s.logger.Error("admin: build oidc page data failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderHTML(c, views.AdminPage(data))
}

// adminOIDCTest 执行「测试连接」：对表单里的 issuer（缺省用已保存值）拉取发现文档，
// 失败时把 provider 的原始错误文本渲染在页面上（M6-4 验收点）。
func (s *Server) adminOIDCTest(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("admin: load oidc config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(c.PostForm(auth.SettingKeyOIDCIssuer)), "/")
	if issuer == "" {
		issuer = cfg.Issuer
	}
	result := ""
	ok = false
	if issuer == "" {
		result = loc.T("admin.oidc.test.no_issuer")
	} else if _, derr := s.oidc.Discover(ctx, issuer); derr != nil {
		// 原样带上 provider 的错误文本，而不是只写日志（DESIGN.md §4.4）。
		s.logger.Info("admin: oidc test connection failed", "issuer", issuer, "error", derr)
		result = loc.T("admin.oidc.test.failed_prefix") + " " + derr.Error()
	} else {
		result = loc.T("admin.oidc.test.ok")
		ok = true
	}
	data, derr := s.oidcAdminData(c, loc, cfg, result, ok)
	if derr != nil {
		s.logger.Error("admin: build oidc page data failed", "error", derr)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderHTML(c, views.AdminPage(data))
}

// adminOIDCSave 保存 OIDC 配置：非敏感值走 PutSetting，client secret 走 PutSecret（加密）。
// 保存后使发现文档缓存失效（DESIGN.md §4.4），写审计，再重定向回页面。
func (s *Server) adminOIDCSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	if _, ok := s.localizer(c); !ok {
		return
	}
	ctx := c.Request.Context()
	now := time.Now().UTC()

	issuer := strings.TrimRight(strings.TrimSpace(c.PostForm(auth.SettingKeyOIDCIssuer)), "/")
	enabled := strings.TrimSpace(c.PostForm(auth.SettingKeyOIDCEnabled)) != ""
	if enabled && issuer != "" {
		if u, err := url.Parse(issuer); err != nil || u.Scheme == "" || u.Host == "" {
			c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=invalid_issuer")
			return
		}
	}
	if enabled && issuer == "" {
		c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=invalid_issuer")
		return
	}

	// 先记录旧 issuer 以便失效缓存。
	old, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("admin: load oidc config failed", "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=save_failed")
		return
	}

	enabledValue := "false"
	if enabled {
		enabledValue = "true"
	}
	writes := []struct{ key, value string }{
		{auth.SettingKeyOIDCEnabled, enabledValue},
	}
	for _, key := range []string{
		auth.SettingKeyOIDCIssuer, auth.SettingKeyOIDCClientID, auth.SettingKeyOIDCScopes,
		auth.SettingKeyOIDCClaimSubject, auth.SettingKeyOIDCClaimEmail,
		auth.SettingKeyOIDCClaimName, auth.SettingKeyOIDCClaimEmailVerified,
	} {
		if raw := strings.TrimSpace(c.PostForm(key)); raw != "" {
			writes = append(writes, struct{ key, value string }{key, raw})
		}
	}
	changed := make([]string, 0, len(writes)+1)
	for _, w := range writes {
		if err := store.PutSetting(ctx, s.db, w.key, w.value, store.Ptr(u.ID), now); err != nil {
			s.logger.Error("admin: save oidc setting failed", "key", w.key, "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=save_failed")
			return
		}
		changed = append(changed, w.key)
	}
	if s.secrets != nil {
		if secret := c.PostForm(auth.SettingKeyOIDCClientSecret); strings.TrimSpace(secret) != "" {
			if err := store.PutSecret(ctx, s.db, s.secrets, auth.SettingKeyOIDCClientSecret, secret, store.Ptr(u.ID), now); err != nil {
				s.logger.Error("admin: save oidc client secret failed", "error", err)
				c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=save_failed")
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
		TargetType: "setting", Detail: map[string]any{"keys": changed, "scope": "oidc"},
	})
	c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=saved")
}

// adminOIDCUnlink 解绑一条外部身份并写审计。解绑后该身份不能再用它登录。
func (s *Server) adminOIDCUnlink(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=unlink_failed")
		return
	}
	ctx := c.Request.Context()
	ident, err := s.identities.ByID(ctx, id)
	if err != nil {
		s.logger.Error("admin: load identity failed", "id", id, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=unlink_failed")
		return
	}
	if err := s.identities.Delete(ctx, id); err != nil {
		s.logger.Error("admin: unlink identity failed", "id", id, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=unlink_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionIdentityUnlink,
		TargetType: "identity", TargetID: store.Ptr(id),
		Detail: map[string]any{"provider": ident.Provider, "subject": ident.Subject, "user_id": ident.UserID},
	})
	c.Redirect(http.StatusSeeOther, "/admin/oidc?notice=unlinked")
}

// oidcAdminData 组装 OIDC 配置页的渲染数据；testResult 为空时不渲染测试结论。
func (s *Server) oidcAdminData(c *gin.Context, loc *i18n.Localizer, cfg *auth.OIDCConfig, testResult string, testOK bool) (views.AdminPageData, error) {
	ctx := c.Request.Context()
	saved, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		return views.AdminPageData{}, err
	}
	secretStatus := loc.T("admin.sensitive.not_configured")
	if s.db != nil {
		if configured, err := store.SecretConfigured(ctx, s.db, auth.SettingKeyOIDCClientSecret); err == nil && configured {
			secretStatus = loc.T("admin.sensitive.configured")
		}
	}
	claimKeys := []string{
		auth.SettingKeyOIDCClaimSubject, auth.SettingKeyOIDCClaimEmail,
		auth.SettingKeyOIDCClaimName, auth.SettingKeyOIDCClaimEmailVerified,
	}
	oidc := &views.OIDCPageData{
		FormAction:              "/admin/oidc",
		TestAction:              "/admin/oidc/test",
		EnabledLabel:            loc.T("admin.oidc.enabled"),
		EnabledHint:             loc.T("admin.oidc.enabled.hint"),
		Enabled:                 cfg.Enabled,
		IssuerLabel:             loc.T("admin.oidc.issuer"),
		IssuerHint:              loc.T("admin.oidc.issuer.hint"),
		IssuerValue:             saved[auth.SettingKeyOIDCIssuer],
		ClientIDLabel:           loc.T("admin.oidc.client_id"),
		ClientIDValue:           saved[auth.SettingKeyOIDCClientID],
		SecretLabel:             loc.T("admin.oidc.client_secret"),
		SecretHint:              loc.T("admin.oidc.client_secret.hint"),
		SecretStatus:            secretStatus,
		ScopesLabel:             loc.T("admin.oidc.scopes"),
		ScopesHint:              loc.T("admin.oidc.scopes.hint"),
		ScopesValue:             saved[auth.SettingKeyOIDCScopes],
		ClaimHeading:            loc.T("admin.oidc.claims"),
		ClaimSubjectLabel:       loc.T("admin.oidc.claim.subject"),
		ClaimSubjectValue:       saved[claimKeys[0]],
		ClaimEmailLabel:         loc.T("admin.oidc.claim.email"),
		ClaimEmailValue:         saved[claimKeys[1]],
		ClaimNameLabel:          loc.T("admin.oidc.claim.name"),
		ClaimNameValue:          saved[claimKeys[2]],
		ClaimEmailVerifiedLabel: loc.T("admin.oidc.claim.email_verified"),
		ClaimEmailVerifiedValue: saved[claimKeys[3]],
		SaveLabel:               loc.T("admin.action.save"),
		TestLabel:               loc.T("admin.oidc.test"),
		TestResult:              testResult,
		TestOK:                  testOK,
		IdentitiesHeading:       loc.T("admin.oidc.identities.heading"),
		IdentitiesIntro:         loc.T("admin.oidc.identities.intro"),
		IdentitiesEmpty:         loc.T("admin.oidc.identities.empty"),
		ColProvider:             loc.T("admin.oidc.col.provider"),
		ColSubject:              loc.T("admin.oidc.col.subject"),
		ColEmail:                loc.T("admin.oidc.col.email"),
		ColUser:                 loc.T("admin.oidc.col.user"),
		ColLinked:               loc.T("admin.oidc.col.linked"),
		ColActions:              loc.T("admin.oidc.col.actions"),
		UnlinkLabel:             loc.T("admin.oidc.unlink"),
	}
	if s.sessions != nil {
		if sess, ok := auth.CurrentSession(c); ok {
			oidc.CSRF = sess.CSRFToken
		}
	}
	if s.identities != nil {
		rows, err := s.identities.ListAll(ctx)
		if err != nil {
			return views.AdminPageData{}, err
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
			oidc.Identities = append(oidc.Identities, views.OIDCIdentityRow{
				Provider:   row.Provider,
				Subject:    row.Subject,
				Email:      email,
				Username:   username,
				LinkedAt:   row.LinkedAt.Format(time.RFC3339),
				UnlinkHref: "/admin/oidc/identities/" + strconv.FormatUint(row.ID, 10) + "/unlink",
			})
		}
	}
	return views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.oidc.title", "/admin/oidc"),
		Heading:    loc.T("admin.oidc.heading"),
		Intro:      loc.T("admin.oidc.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/oidc"),
		Notice:     s.oidcNotice(loc, c.Query("notice"), c),
		OIDCPage:   true,
		OIDC:       oidc,
	}, nil
}

// oidcNotice 把重定向回带的 notice 码翻成文案。
func (s *Server) oidcNotice(loc *i18n.Localizer, code string, _ *gin.Context) string {
	switch code {
	case "saved":
		return loc.T("admin.oidc.notice.saved")
	case "unlinked":
		return loc.T("admin.oidc.notice.unlinked")
	case "invalid_issuer":
		return loc.T("admin.oidc.notice.invalid_issuer")
	case "save_failed", "unlink_failed":
		return loc.T("admin.oidc.notice.failed")
	default:
		return ""
	}
}
