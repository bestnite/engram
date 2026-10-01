package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// 管理面板的 API Key 总览（DESIGN.md §8.4；AGENTS.md §5 M6-9）。
//
// 安全底线：
//   - 路由在 adminRoutes() 清单里，非 admin 一律 403（守卫先于 handler）。
//   - 列表只展示元信息：名称、前缀、scopes、最后使用、过期、状态。库里只存 sha256，
//     页面既不渲染 key_hash，也不可能有明文——模型里就没有明文列。
//   - 撤销是写操作，过 CSRF 并写审计；审计详情不带任何 key 内容。
//   - 列表分页（每页 adminKeysPageSize），绝不一次拉全表。

// adminKeysPageSize 是 API Key 总览每页行数。
const adminKeysPageSize = 50

// keysNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) keysNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "revoked":
		return loc.T("admin.keys.notice.revoked")
	case "revoke_failed":
		return loc.T("admin.keys.notice.revoke_failed")
	case "invalid_key":
		return loc.T("admin.keys.notice.invalid_key")
	default:
		return ""
	}
}

// keyStateLabel 把存储状态翻成显示文案。
func keyStateLabel(loc *i18n.Localizer, state string) string {
	switch state {
	case store.APIKeyStateRevoked:
		return loc.T("admin.keys.state.revoked")
	case store.APIKeyStateExpired:
		return loc.T("admin.keys.state.expired")
	default:
		return loc.T("admin.keys.state.active")
	}
}

// adminAPIKeysPage 渲染全用户 API Key 总览：分页列出每把 key 的元信息。
func (s *Server) adminAPIKeysPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)
	page := pageParam(c.Query("page"))

	keys, total, err := store.NewAPIKeyStore(s.db).ListAll(ctx, adminKeysPageSize, (page-1)*adminKeysPageSize)
	if err != nil {
		s.logger.Error("admin: list api keys failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 批量取归属用户名，只解析当前页出现的 user_id（最多一页）。
	names := make(map[uint64]string)
	if s.users != nil {
		for i := range keys {
			uid := keys[i].UserID
			if _, seen := names[uid]; seen {
				continue
			}
			if u, err := s.users.ByID(ctx, uid); err == nil {
				names[uid] = u.Username
			}
		}
	}

	now := time.Now().UTC()
	rows := make([]views.AdminAPIKeyRow, 0, len(keys))
	for i := range keys {
		k := keys[i]
		owner := "#" + strconv.FormatUint(k.UserID, 10)
		if name, ok := names[k.UserID]; ok {
			owner = name
		}
		lastUsed := loc.T("admin.keys.last_used_never")
		if k.LastUsedAt != nil {
			lastUsed = k.LastUsedAt.In(userLoc).Format("2006-01-02 15:04")
		}
		expires := loc.T("admin.keys.expires_never")
		if k.ExpiresAt != nil {
			expires = k.ExpiresAt.In(userLoc).Format("2006-01-02")
		}
		state := store.APIKeyState(&k, now)
		rows = append(rows, views.AdminAPIKeyRow{
			ID:         strconv.FormatUint(k.ID, 10),
			Owner:      owner,
			Name:       k.Name,
			Prefix:     k.Prefix,
			Scopes:     strings.Join(store.ParseScopes(k.Scopes), ", "),
			LastUsed:   lastUsed,
			Expires:    expires,
			State:      state,
			StateLabel: keyStateLabel(loc, state),
			CanRevoke:  state == store.APIKeyStateActive,
			RevokeHref: "/admin/api-keys/" + strconv.FormatUint(k.ID, 10) + "/revoke",
		})
	}

	pages := int((total + int64(adminKeysPageSize) - 1) / int64(adminKeysPageSize))
	if pages < 1 {
		pages = 1
	}
	prevHref, nextHref := "", ""
	if page > 1 {
		prevHref = "/admin/api-keys?page=" + strconv.Itoa(page-1)
	}
	if page < pages {
		nextHref = "/admin/api-keys?page=" + strconv.Itoa(page+1)
	}
	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}

	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.keys.title", "/admin/api-keys"),
		Heading:    loc.T("admin.keys.heading"),
		Intro:      loc.T("admin.keys.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/api-keys"),
		Notice:     s.keysNotice(loc, c.Query("notice")),
		CSRF:       csrf,

		APIKeysPage: true,
		APIKeyRows:  rows,

		ColKeyName:     loc.T("admin.keys.col.name"),
		ColKeyOwner:    loc.T("admin.keys.col.owner"),
		ColKeyPrefix:   loc.T("admin.keys.col.prefix"),
		ColKeyScopes:   loc.T("admin.keys.col.scopes"),
		ColKeyLastUsed: loc.T("admin.keys.col.last_used"),
		ColKeyExpires:  loc.T("admin.keys.col.expires"),
		ColKeyState:    loc.T("admin.keys.col.state"),
		ColKeyActions:  loc.T("admin.keys.col.actions"),
		KeyRevokeLabel: loc.T("admin.keys.revoke"),

		Page: page, Pages: pages, Total: total,
		PrevHref: prevHref, NextHref: nextHref,
		PrevLabel: loc.T("admin.jobs.prev"), NextLabel: loc.T("admin.jobs.next"),
		EmptyLabel: loc.T("admin.keys.empty"),
	}))
}

// adminAPIKeyRevoke 撤销任意用户的一把 key（管理员动作）；幂等，重复撤销仍命中同一行。
func (s *Server) adminAPIKeyRevoke(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/admin/api-keys?notice=invalid_key")
		return
	}
	ctx := c.Request.Context()
	if err := store.NewAPIKeyStore(s.db).RevokeByID(ctx, id, time.Now().UTC()); err != nil {
		notice := "revoke_failed"
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			notice = "invalid_key"
		}
		c.Redirect(http.StatusSeeOther, "/admin/api-keys?notice="+notice)
		return
	}
	// 审计只记「谁撤销了哪把 key」；不写任何 key 内容（明文不存在，哈希也不该进审计）。
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionAPIKeyRevoke,
		TargetType: "api_key",
		TargetID:   store.Ptr(id),
	})
	c.Redirect(http.StatusSeeOther, "/admin/api-keys?notice=revoked")
}
