package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 用户级 API Key 管理页（M4-10，DESIGN.md §7.2、§8.1）。
//
// 与 /admin/api-keys 的分工：管理面板是全用户总览（可撤销、不能创建）；本页是登录用户
// 自己的 key 列表，能创建、能撤销本人的 key。安全底线：
//   - 一律按当前会话用户的 id 取列表与撤销，别人的 key 既不列出也不可撤销（store.Revoke
//     的归属校验把「不存在」与「不属于我」都归为 ErrAPIKeyNotFound，映射成 404）。
//   - 明文只在创建成功的那次响应里渲染一次，绝不落库、绝不在后续 GET 出现。
//   - 创建与撤销都是写操作，过 CSRF 中间件（DESIGN.md §4.3），并各写一条审计。

// keyScopesFor 返回该角色在创建表单里可勾选的 scope 档位，顺序与 store 的规范顺序一致
// （DESIGN.md §7.2）。普通用户看不到 admin：这只是辅助防线，服务端拒绝才是真正的边界
// （见 keysCreate）。展示名走语言包 keys.scope.<value>，不在代码里写死文案。
func keyScopesFor(user *store.User) []string {
	scopes := []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview, store.ScopeKeys}
	if user != nil && user.Role == store.RoleAdmin {
		scopes = append(scopes, store.ScopeAdmin)
	}
	return scopes
}

// scopesIncludeAdmin 判断表单提交的 scope 里是否含 admin（按原值匹配，未归一化）。
// 取值合法性由 store.NormalizeScopes 负责，这里只关心“是否试图授予 admin”。
func scopesIncludeAdmin(scopes []string) bool {
	for _, s := range scopes {
		if strings.TrimSpace(s) == store.ScopeAdmin {
			return true
		}
	}
	return false
}

// keysPage 渲染「我的 API Key」页；匿名访问被重定向到登录页（requireUser）。
func (s *Server) keysPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.renderKeys(c, loc, user, http.StatusOK, "", "")
}

// keysCreate 创建一把属于当前用户的 key（M4-10）。
//
// 校验顺序：名称非空 -> 至少勾选一个 scope -> scope 合法 -> 可选过期日期合法。
// 任何一步失败都以本地化提示重渲染（4xx）且不写库。成功后把明文就地渲染一次：
// 库里只存 sha256，错过这一次只能重新创建（DESIGN.md §7.2）。
func (s *Server) keysCreate(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		s.renderKeys(c, loc, user, http.StatusBadRequest, loc.T("keys.error.name_required"), "")
		return
	}

	// 空选择必须拒绝：store.NormalizeScopes 对空输入会回落到默认 read，本页要求用户显式选择。
	selected := c.PostFormArray("scopes")
	if len(selected) == 0 {
		s.renderKeys(c, loc, user, http.StatusBadRequest, loc.T("keys.error.scopes_required"), "")
		return
	}
	normalized, err := store.NormalizeScopes(selected)
	if err != nil {
		s.renderKeys(c, loc, user, http.StatusBadRequest, loc.T("keys.error.scopes_invalid"), "")
		return
	}
	// admin scope 只能发给管理员账号：非管理员经任何路径提交一律拒绝，且不落库（DESIGN.md
	// §7.2）。表单隐藏 admin 选项只是辅助，服务端不信任任何提交上来的表单。钤制放在这里
	// 是因为只有传输层知道 actor 的角色。
	if scopesIncludeAdmin(selected) && user.Role != store.RoleAdmin {
		s.renderKeys(c, loc, user, http.StatusForbidden, loc.T("keys.error.admin_forbidden"), "")
		return
	}

	var expiresAt *time.Time
	expiresDetail := ""
	if raw := strings.TrimSpace(c.PostForm("expires_at")); raw != "" {
		day, err := time.Parse("2006-01-02", raw)
		if err != nil {
			s.renderKeys(c, loc, user, http.StatusBadRequest, loc.T("keys.error.expiry_invalid"), "")
			return
		}
		// 选中的日期按「当天结束」生效，避免选了今天却立刻过期（与分享链接一致）。
		end := day.AddDate(0, 0, 1)
		expiresAt = &end
		expiresDetail = end.UTC().Format(time.RFC3339)
	}

	created, err := store.NewAPIKeyStore(s.db).Create(ctx, store.CreateAPIKeyParams{
		UserID:    user.ID,
		Name:      name,
		Scopes:    store.ParseScopes(normalized),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		s.logger.Error("create api key failed", "user_id", user.ID, "error", err)
		s.renderKeys(c, loc, user, http.StatusInternalServerError, loc.T("keys.error.create_failed"), "")
		return
	}
	// 审计只记元信息，绝不写明文或哈希。
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionAPIKeyCreate,
		TargetType: "api_key",
		TargetID:   store.Ptr(created.Key.ID),
		Detail:     map[string]any{"name": name, "scopes": normalized, "expires_at": expiresDetail},
	})
	s.renderKeys(c, loc, user, http.StatusOK, "", created.Plaintext)
}

// keysRevoke 撤销当前用户的一把 key（M4-10）。
//
// store.Revoke 按 (id, user_id) 限定归属：key 不存在或不属于调用者都返回 ErrAPIKeyNotFound，
// 这里统一映射成 404 而不是 403，避免用状态码差异泄露他人 key 的存在性。撤销即时生效。
func (s *Server) keysRevoke(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	if err := store.NewAPIKeyStore(s.db).Revoke(ctx, user.ID, id, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		s.logger.Error("revoke api key failed", "user_id", user.ID, "key_id", id, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionAPIKeyRevoke,
		TargetType: "api_key",
		TargetID:   store.Ptr(id),
	})
	c.Redirect(http.StatusSeeOther, "/settings/keys")
}

// renderKeys 装配「我的 API Key」页数据并写出；status 用于把校验失败渲染成 4xx。
// newPlaintext 非空时显示一次性明文横幅（只可能来自创建成功的响应）。
func (s *Server) renderKeys(c *gin.Context, loc *i18n.Localizer, user *store.User, status int, errMsg, newPlaintext string) {
	ctx := c.Request.Context()
	keys, err := store.NewAPIKeyStore(s.db).ListByUser(ctx, user.ID)
	if err != nil {
		s.logger.Error("list api keys failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	userLoc := auditLocation(user)
	now := time.Now().UTC()
	rows := make([]views.APIKeyRow, 0, len(keys))
	for i := range keys {
		k := keys[i]
		lastUsed := loc.T("keys.last_used_never")
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.In(userLoc).Format("2006-01-02 15:04")
		}
		expires := loc.T("keys.expires_never")
		if k.ExpiresAt != nil {
			expires = k.ExpiresAt.In(userLoc).Format("2006-01-02")
		}
		state := store.APIKeyState(&k, now)
		rows = append(rows, views.APIKeyRow{
			ID:         strconv.FormatUint(k.ID, 10),
			Name:       k.Name,
			Prefix:     k.Prefix,
			Scopes:     strings.Join(store.ParseScopes(k.Scopes), ", "),
			LastUsed:   lastUsed,
			Expires:    expires,
			StateLabel: keysStateLabel(loc, state),
			CanRevoke:  state == store.APIKeyStateActive,
			RevokeHref: fmt.Sprintf("/settings/keys/%d/revoke", k.ID),
		})
	}

	scopeOptions := make([]views.ScopeOption, 0, len(keyScopesFor(user)))
	for _, sc := range keyScopesFor(user) {
		scopeOptions = append(scopeOptions, views.ScopeOption{
			Value: sc,
			Label: loc.T("keys.scope." + sc),
		})
	}

	data := views.KeysData{
		Layout:       s.pageLayout(c, loc, "keys.title"),
		Heading:      loc.T("keys.heading"),
		Intro:        loc.T("keys.intro"),
		ErrorMessage: errMsg,

		NewKeyNotice: loc.T("keys.new.notice"),
		NewKeyPlain:  newPlaintext,

		ColName:     loc.T("keys.col.name"),
		ColPrefix:   loc.T("keys.col.prefix"),
		ColScopes:   loc.T("keys.col.scopes"),
		ColLastUsed: loc.T("keys.col.last_used"),
		ColExpires:  loc.T("keys.col.expires"),
		ColState:    loc.T("keys.col.state"),
		ColActions:  loc.T("keys.col.actions"),
		Rows:        rows,
		EmptyText:   loc.T("keys.empty"),
		RevokeLabel: loc.T("keys.revoke"),

		CreateHeading:   loc.T("keys.create.heading"),
		NameLabel:       loc.T("keys.create.name_label"),
		NamePlaceholder: loc.T("keys.create.name_placeholder"),
		ScopesLabel:     loc.T("keys.create.scopes_label"),
		ScopesHint:      loc.T("keys.create.scopes_hint"),
		ScopeOptions:    scopeOptions,
		ExpiresLabel:    loc.T("keys.create.expires_label"),
		ExpiresHint:     loc.T("keys.create.expires_hint"),
		CreateSubmit:    loc.T("keys.create.submit"),
		CreateAction:    "/settings/keys",
		CSRF:            sessionCSRF(c),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.KeysPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// keysStateLabel 把存储状态翻成本页的显示文案（独立于管理面板的 admin.keys.* 文案）。
func keysStateLabel(loc *i18n.Localizer, state string) string {
	switch state {
	case store.APIKeyStateRevoked:
		return loc.T("keys.state.revoked")
	case store.APIKeyStateExpired:
		return loc.T("keys.state.expired")
	default:
		return loc.T("keys.state.active")
	}
}
