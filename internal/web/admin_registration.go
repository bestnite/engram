package web

import (
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

// 管理面板的注册与邀请（DESIGN.md §4.2、§8.4；AGENTS.md §5 M6-3）。
//
// 免重启生效的关键：settings 表按请求现读（registerSubmit 每次注册都 LoadSettings），
// 所以这里写库后，下一次注册尝试立即按新策略判定，不需要重启进程。
// 所有写路由都在 adminRoutes() 清单里（非 admin 全量 403 的测试据此覆盖），过 CSRF 并写审计。

// regNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) regNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "saved":
		return loc.T("admin.registration.notice.saved")
	case "invalid_policy":
		return loc.T("admin.registration.notice.invalid_policy")
	case "save_failed":
		return loc.T("admin.registration.notice.save_failed")
	case "invite_created":
		return loc.T("admin.registration.notice.invite_created")
	case "invite_revoked":
		return loc.T("admin.registration.notice.invite_revoked")
	case "invite_invalid":
		return loc.T("admin.registration.notice.invite_invalid")
	case "invite_create_failed":
		return loc.T("admin.registration.notice.invite_create_failed")
	default:
		return ""
	}
}

// policyOptions 返回策略下拉项，顺序固定（open / invite / closed），标出当前生效值。
func policyOptions(loc *i18n.Localizer, current string) []views.AdminPolicyOption {
	defs := []struct{ value, labelKey string }{
		{auth.PolicyOpen, "admin.registration.policy.open"},
		{auth.PolicyInvite, "admin.registration.policy.invite"},
		{auth.PolicyClosed, "admin.registration.policy.closed"},
	}
	out := make([]views.AdminPolicyOption, 0, len(defs))
	for _, d := range defs {
		out = append(out, views.AdminPolicyOption{
			Value:   d.value,
			Label:   loc.T(d.labelKey),
			Checked: current == d.value,
		})
	}
	return out
}

// inviteStatus 把一条邀请归成 active / used / expired 三态。
func inviteStatus(inv store.Invite, now time.Time) string {
	if inv.UsedAt != nil {
		return "used"
	}
	if inv.ExpiresAt != nil && !inv.ExpiresAt.After(now) {
		return "expired"
	}
	return "active"
}

// inviteRow 把一条邀请渲染成表格行；used_by 解析成用户名，无法解析时回落到十进制 id。
func (s *Server) inviteRow(c *gin.Context, loc *i18n.Localizer, inv store.Invite, now time.Time) views.AdminInviteRow {
	ctx := c.Request.Context()
	email := ""
	if inv.Email != nil {
		email = *inv.Email
	}
	usedBy := ""
	if inv.UsedBy != nil {
		if u, err := s.users.ByID(ctx, *inv.UsedBy); err == nil && u != nil {
			usedBy = u.Username
		} else {
			usedBy = strconv.FormatUint(*inv.UsedBy, 10)
		}
	}
	createdAt := inv.CreatedAt.UTC().Format("2006-01-02 15:04")
	expires := ""
	if inv.ExpiresAt != nil {
		expires = inv.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	status := inviteStatus(inv, now)
	return views.AdminInviteRow{
		ID:          strconv.FormatUint(inv.ID, 10),
		Token:       inv.Token,
		Link:        "/register?invite=" + inv.Token,
		Email:       email,
		RoleLabel:   roleLabel(loc, inv.Role),
		StatusValue: status,
		StatusLabel: loc.T("admin.registration.status." + status),
		CreatedAt:   createdAt,
		ExpiresAt:   expires,
		UsedAt:      formatTimePtr(inv.UsedAt),
		UsedBy:      usedBy,
		Active:      status == "active",
		RevokeHref:  "/admin/invites/" + strconv.FormatUint(inv.ID, 10) + "/revoke",
	}
}

// formatTimePtr 把可空时间格式化成展示串；nil 返回空。
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// adminRegistrationPage 渲染注册与邀请页：策略 / 白名单 / 邀请列表与创建表单。
func (s *Server) adminRegistrationPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	settings, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("admin: load settings failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	policy := auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy])
	// 白名单原样回显（兼容 JSON 数组与分隔符两种写法），保存时再归一化。
	allowlistRaw := strings.TrimSpace(settings[auth.SettingKeyEmailAllowlist])

	var invites []store.Invite
	if s.invites != nil {
		invites, err = s.invites.List(ctx)
		if err != nil {
			s.logger.Error("admin: list invites failed", "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}
	now := time.Now().UTC()
	rows := make([]views.AdminInviteRow, 0, len(invites))
	for _, inv := range invites {
		rows = append(rows, s.inviteRow(c, loc, inv, now))
	}

	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:           s.adminLayout(c, loc, "admin.registration.title", "/admin/registration"),
		Heading:          loc.T("admin.registration.heading"),
		Intro:            loc.T("admin.registration.intro"),
		NavHeading:       loc.T("admin.nav.heading"),
		Nav:              s.adminNav(loc, "/admin/registration"),
		Notice:           s.regNotice(loc, c.Query("notice")),
		RegistrationPage: true,
		CSRF:             csrf,

		PolicyHeading:         loc.T("admin.registration.policy_heading"),
		PolicyIntro:           loc.T("admin.registration.policy_intro"),
		PolicyLabel:           loc.T("admin.registration.policy_label"),
		PolicyOptions:         policyOptions(loc, policy),
		AllowlistLabel:        loc.T("admin.registration.allowlist_label"),
		AllowlistHint:         loc.T("admin.registration.allowlist_hint"),
		AllowlistValue:        allowlistRaw,
		RegistrationSaveLabel: loc.T("admin.action.save"),

		InvitesHeading:   loc.T("admin.registration.invites_heading"),
		InvitesIntro:     loc.T("admin.registration.invites_intro"),
		InvitesEmpty:     loc.T("admin.registration.invites_empty"),
		ColInviteEmail:   loc.T("admin.registration.col.email"),
		ColInviteRole:    loc.T("admin.registration.col.role"),
		ColInviteStatus:  loc.T("admin.registration.col.status"),
		ColInviteCreated: loc.T("admin.registration.col.created"),
		ColInviteExpires: loc.T("admin.registration.col.expires"),
		ColInviteUsedBy:  loc.T("admin.registration.col.used_by"),
		ColInviteActions: loc.T("admin.registration.col.actions"),
		ColInviteToken:   loc.T("admin.registration.col.token"),
		Invites:          rows,

		InviteCreateHeading: loc.T("admin.registration.invite_create_heading"),
		InviteEmailLabel:    loc.T("admin.registration.invite_email"),
		InviteRoleLabel:     loc.T("admin.registration.invite_role"),
		InviteExpiryLabel:   loc.T("admin.registration.invite_expiry"),
		InviteExpiryHint:    loc.T("admin.registration.invite_expiry_hint"),
		InviteCreateSubmit:  loc.T("admin.registration.invite_create_submit"),
		InviteRevokeLabel:   loc.T("admin.registration.invite_revoke"),
		InviteRoles:         roleOptions(loc),
	}))
}

// adminRegistrationSave 写入注册策略与邮箱域名白名单（均为普通 settings），写审计后重定向。
// 下一次注册请求会现读这两个键，因此策略立即生效（M6-3 验收）。
func (s *Server) adminRegistrationSave(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	ctx := c.Request.Context()
	policy := strings.TrimSpace(c.PostForm("registration_policy"))
	switch policy {
	case auth.PolicyOpen, auth.PolicyInvite, auth.PolicyClosed:
	default:
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invalid_policy")
		return
	}
	// 白名单归一化后以逗号分隔存储（ParseEmailAllowlist 兼容该写法）；空串表示不限制。
	allowlist := strings.Join(auth.ParseEmailAllowlist(c.PostForm("registration_email_domains")), ",")
	now := time.Now().UTC()

	if err := store.PutSetting(ctx, s.db, auth.SettingKeyRegistrationPolicy, policy, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("admin: save registration policy failed", "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=save_failed")
		return
	}
	if err := store.PutSetting(ctx, s.db, auth.SettingKeyEmailAllowlist, allowlist, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("admin: save email allowlist failed", "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionSettingUpdate,
		TargetType: "setting",
		Detail:     map[string]any{"keys": []string{auth.SettingKeyRegistrationPolicy, auth.SettingKeyEmailAllowlist}, "policy": policy},
	})
	s.redirectRegistration(c, "saved")
}

// adminInviteCreate 创建一条邀请：可选限定邮箱、角色与有效天数。
func (s *Server) adminInviteCreate(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	ctx := c.Request.Context()
	if s.invites == nil {
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_create_failed")
		return
	}
	role := strings.TrimSpace(c.PostForm("role"))
	if role != store.RoleAdmin && role != store.RoleUser {
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_create_failed")
		return
	}
	inv := &store.Invite{Role: role, CreatedBy: store.Ptr(actor.ID), CreatedAt: time.Now().UTC()}
	if email := strings.ToLower(strings.TrimSpace(c.PostForm("email"))); email != "" {
		inv.Email = &email
	}
	// 有效天数：留空或 0 表示不过期；非法值拒绝而不是静默当成不过期。
	if raw := strings.TrimSpace(c.PostForm("expires_days")); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_create_failed")
			return
		}
		if days > 0 {
			exp := time.Now().UTC().AddDate(0, 0, days)
			inv.ExpiresAt = &exp
		}
	}
	if err := s.invites.Create(ctx, inv); err != nil {
		s.logger.Error("admin: create invite failed", "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_create_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteCreate,
		TargetType: "invite", TargetID: store.Ptr(inv.ID),
		// 审计不记录 token 明文（等价于凭据）。
		Detail: map[string]any{"role": inv.Role, "has_email": inv.Email != nil, "expires": inv.ExpiresAt != nil},
	})
	s.redirectRegistration(c, "invite_created")
}

// adminInviteRevoke 撤销一条邀请（删除整行，DESIGN.md §2.2），写审计。
func (s *Server) adminInviteRevoke(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	ctx := c.Request.Context()
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 || s.invites == nil {
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_invalid")
		return
	}
	if err := s.invites.Revoke(ctx, id); err != nil {
		if err == store.ErrInviteNotFound {
			c.Redirect(http.StatusSeeOther, "/admin/registration?notice=invite_invalid")
			return
		}
		s.logger.Error("admin: revoke invite failed", "invite_id", id, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/registration?notice=save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteRevoke,
		TargetType: "invite", TargetID: store.Ptr(id),
	})
	s.redirectRegistration(c, "invite_revoked")
}

// redirectRegistration 回到注册与邀请页并带上 notice。
func (s *Server) redirectRegistration(c *gin.Context, notice string) {
	c.Redirect(http.StatusSeeOther, "/admin/registration?notice="+notice)
}
