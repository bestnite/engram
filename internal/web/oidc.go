package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/store"
)

// OIDC 登录流程（DESIGN.md §4.4、§4.5；AGENTS.md §5 M1-11）。
//
// 只实现协议交互与路由，身份绑定全部交给 auth.IdentityLinkService（oauthlink.go）：
//   GET /auth/oidc/start     生成 state / nonce / PKCE，跳转到 provider 授权端点
//   GET /auth/oidc/callback  校验 state（一次性）、换 token、校验 nonce 与签名、绑定并开会话
//
// OIDC 默认关闭：配置不完整时这两个路由返回 404，登录页也不显示入口（不允许半开）。

// oidcLoadConfig 读取生效的 OIDC 配置；数据库未装配时视为未配置。
func (s *Server) oidcLoadConfig(c *gin.Context) (*auth.OIDCConfig, error) {
	if s.db == nil {
		return &auth.OIDCConfig{}, nil
	}
	return auth.LoadOIDCConfig(c.Request.Context(), s.db, s.secrets)
}

// oidcRedirectURI 返回回调地址：配置了 BASE_URL 就用它，否则按当前请求推导
// （本地 http 开发与 httptest 依赖后者）。
func (s *Server) oidcRedirectURI(c *gin.Context) string {
	if base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/"); base != "" {
		return base + "/auth/oidc/callback"
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + c.Request.Host + "/auth/oidc/callback"
}

// oidcStart 发起一次 OIDC 登录：发现文档 → state/nonce/PKCE → 跳授权端点。
func (s *Server) oidcStart(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("oidc: load config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !cfg.Usable() {
		// 未启用或配置不完整：路由不可用，与登录页不显示入口保持一致。
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	redirectURI := s.oidcRedirectURI(c)
	party, err := s.oidc.Client(ctx, cfg, redirectURI)
	if err != nil {
		s.logger.Error("oidc: discovery failed", "issuer", cfg.Issuer, "error", err)
		s.renderLogin(c, loc, http.StatusBadGateway, loc.T("auth.error.oidc_unavailable"))
		return
	}
	state, err := auth.NewState()
	if err != nil {
		s.logger.Error("oidc: generate state failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	nonce, err := auth.NewState()
	if err != nil {
		s.logger.Error("oidc: generate nonce failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	pkce, err := auth.NewPKCE()
	if err != nil {
		s.logger.Error("oidc: generate pkce failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.oidc.PutPending(state, auth.PendingAuth{
		Nonce:       nonce,
		Verifier:    pkce.Verifier,
		RedirectURI: redirectURI,
		ExpiresAt:   time.Now().UTC().Add(10 * time.Minute),
	})
	authURL := auth.BuildAuthURL(party, state, nonce, pkce.Challenge)
	c.Redirect(http.StatusFound, authURL)
}

// oidcCallback 处理授权回调：校验 state → 换 token → 校验 ID Token → 绑定 → 开会话。
func (s *Server) oidcCallback(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	cfg, err := s.oidcLoadConfig(c)
	if err != nil {
		s.logger.Error("oidc: load config failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !cfg.Usable() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if s.identityLink == nil {
		s.logger.Error("oidc: identity link service is not wired")
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// provider 侧主动返回错误（用户取消、scope 不足等）：原样带进日志，页面给本地化提示。
	if perr := strings.TrimSpace(c.Query("error")); perr != "" {
		s.logger.Info("oidc: provider returned an error", "error", perr, "description", c.Query("error_description"))
		s.renderLogin(c, loc, http.StatusUnauthorized, loc.T("auth.error.oidc_failed"))
		return
	}

	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		s.renderLogin(c, loc, http.StatusBadRequest, loc.T("auth.error.oidc_state"))
		return
	}
	// state 一次性：未知（伪造或重放）或已过期都在这里被拒（M1-11 负例）。
	pending, ok := s.oidc.TakePending(state)
	if !ok {
		s.logger.Info("oidc: callback with an unknown or expired state")
		s.oidcRecordFailure(ctx, "state")
		s.renderLogin(c, loc, http.StatusBadRequest, loc.T("auth.error.oidc_state"))
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		s.renderLogin(c, loc, http.StatusBadRequest, loc.T("auth.error.oidc_failed"))
		return
	}

	// 从 state 取回发起登录时保存的 redirect_uri / PKCE verifier / nonce。
	party, err := s.oidc.Client(ctx, cfg, pending.RedirectURI)
	if err != nil {
		s.logger.Error("oidc: discovery failed during callback", "issuer", cfg.Issuer, "error", err)
		s.renderLogin(c, loc, http.StatusBadGateway, loc.T("auth.error.oidc_unavailable"))
		return
	}
	claims, err := s.oidc.ExchangeCode(ctx, party, code, pending.Verifier, pending.Nonce)
	if err != nil {
		s.logger.Error("oidc: token exchange or id_token verification failed", "error", err)
		s.renderLogin(c, loc, http.StatusUnauthorized, loc.T("auth.error.oidc_failed"))
		return
	}
	profile := cfg.ProfileFromClaims(party.Issuer(), claims.Claims)

	policy := auth.PolicyClosed
	if settings, err := store.LoadSettings(ctx, s.db); err == nil {
		policy = auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
	}
	u, decision, err := s.identityLink.Resolve(ctx, profile, policy)
	if err != nil {
		if err == auth.ErrIdentityLinkDenied {
			// closed 策略下没有既有绑定也无法自动匹配：提示联系管理员（DESIGN.md §4.5.3）。
			s.logger.Info("oidc: login denied by registration policy", "provider", profile.Provider)
			s.renderLogin(c, loc, http.StatusForbidden, loc.T("auth.error.oidc_link_denied"))
			return
		}
		s.logger.Error("oidc: identity link failed", "error", err)
		s.renderLogin(c, loc, http.StatusUnauthorized, loc.T("auth.error.oidc_failed"))
		return
	}
	if _, err := s.sessions.StartSession(ctx, c, u.ID); err != nil {
		s.logger.Error("oidc: start session failed", "user_id", u.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID),
		Action: store.ActionUserLoginSucceeded,
		Detail: map[string]any{"method": "oidc", "provider": profile.Provider, "decision": string(decision.Kind)},
	})
	c.Redirect(http.StatusSeeOther, "/")
}

// oidcRecordFailure 给 state 校验失败留一条审计（无 user_id：此时身份未知）。
func (s *Server) oidcRecordFailure(ctx context.Context, reason string) {
	s.audit(ctx, store.AuditEntry{
		Action:     store.ActionUserLoginFailed,
		TargetType: "oidc",
		Detail:     map[string]any{"reason": reason},
	})
}
