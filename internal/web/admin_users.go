package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// 管理面板的用户管理（DESIGN.md §8.4；AGENTS.md §5 M6-2）。
//
// 安全底线：
//   - 所有路由都在 adminRoutes() 清单里，非 admin 一律 403（守卫先于 handler）。
//   - 所有写操作过 CSRF 中间件并写审计。
//   - 危险动作（删除 / 改角色 / 重置密码）要求确认字段 confirm=1，模板用 <details>
//     让它们多一次点击（模板层确认），handler 再兜一层服务端校验。
//   - 不能删除或禁用自己，也不能让最后一个可登录管理员消失。

// usersNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) usersNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "created":
		return loc.T("admin.users.notice.created")
	case "create_failed":
		return loc.T("admin.users.notice.create_failed")
	case "updated":
		return loc.T("admin.users.notice.updated")
	case "deleted":
		return loc.T("admin.users.notice.deleted")
	case "last_admin":
		return loc.T("admin.users.notice.last_admin")
	case "self_forbidden":
		return loc.T("admin.users.notice.self_forbidden")
	case "confirm_required":
		return loc.T("admin.users.notice.confirm_required")
	case "invalid_user":
		return loc.T("admin.users.notice.invalid_user")
	case "invalid_role":
		return loc.T("admin.users.notice.invalid_role")
	case "save_failed":
		return loc.T("admin.users.notice.save_failed")
	default:
		return ""
	}
}

// roleLabel / statusLabel 把存储取值翻成显示文案。
func roleLabel(loc *i18n.Localizer, role string) string {
	if role == store.RoleAdmin {
		return loc.T("admin.users.role.admin")
	}
	return loc.T("admin.users.role.user")
}

func statusLabel(loc *i18n.Localizer, status string) string {
	if status == store.StatusDisabled {
		return loc.T("admin.users.status.disabled")
	}
	return loc.T("admin.users.status.active")
}

// roleOptions 返回角色下拉项，顺序固定（admin 在前）。
func roleOptions(loc *i18n.Localizer) []views.AdminRoleOption {
	return []views.AdminRoleOption{
		{Value: store.RoleUser, Label: loc.T("admin.users.role.user")},
		{Value: store.RoleAdmin, Label: loc.T("admin.users.role.admin")},
	}
}

// pageParam 解析 page 查询参数，非法或越界回落到第 1 页。
func pageParam(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// adminUsersPage 渲染用户列表：搜索、分页、每用户用量计数。
func (s *Server) adminUsersPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	s.renderUsersPage(c, loc, s.usersNotice(loc, c.Query("notice")), "", strings.TrimSpace(c.Query("q")), pageParam(c.Query("page")))
}

// renderUsersPage 构造并渲染用户管理页；tempPassword 非空时在页面上一次性展示（重置密码后）。
func (s *Server) renderUsersPage(c *gin.Context, loc *i18n.Localizer, notice, tempPassword, query string, page int) {
	ctx := c.Request.Context()
	size := store.AdminListUsersPageSize
	users, total, err := s.users.ListForAdmin(ctx, query, page, size)
	if err != nil {
		s.logger.Error("admin: list users failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	var currentID uint64
	if u, ok := auth.CurrentUser(c); ok {
		currentID = u.ID
	}
	rows := make([]views.AdminUserRow, 0, len(users))
	for _, u := range users {
		usage, err := s.users.UsageCounts(ctx, u.ID)
		if err != nil {
			s.logger.Error("admin: usage counts failed", "user_id", u.ID, "error", err)
		}
		base := "/admin/users/" + strconv.FormatUint(u.ID, 10)
		rows = append(rows, views.AdminUserRow{
			ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
			Role: u.Role, RoleLabel: roleLabel(loc, u.Role),
			Status: u.Status, StatusLabel: statusLabel(loc, u.Status),
			Decks: usage.Decks, Cards: usage.Cards, Reviews: usage.Reviews,
			IsSelf:     u.ID == currentID,
			HrefStatus: base + "/status", HrefRole: base + "/role",
			HrefPassword: base + "/password", HrefLogout: base + "/logout", HrefDelete: base + "/delete",
		})
	}
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	prevHref, nextHref := "", ""
	if page > 1 {
		prevHref = usersPageHref(query, page-1)
	}
	if page < pages {
		nextHref = usersPageHref(query, page+1)
	}
	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.users.title", "/admin/users"),
		Heading:    loc.T("admin.users.heading"),
		Intro:      loc.T("admin.users.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/users"),
		UsersPage:  true,
		Notice:     notice,
		CSRF:       csrf,

		SearchLabel:       loc.T("admin.users.search_label"),
		SearchPlaceholder: loc.T("admin.users.search_placeholder"),
		SearchSubmit:      loc.T("admin.users.search_submit"),
		SearchQuery:       query,

		Users: rows, Page: page, Pages: pages, Total: total,
		ColUser: loc.T("admin.users.col.user"), ColEmail: loc.T("admin.users.col.email"),
		ColRole: loc.T("admin.users.col.role"), ColStatus: loc.T("admin.users.col.status"),
		ColDecks: loc.T("admin.users.col.decks"), ColCards: loc.T("admin.users.col.cards"),
		ColReviews: loc.T("admin.users.col.reviews"), ColActions: loc.T("admin.users.col.actions"),

		CreateHeading:       loc.T("admin.users.create_heading"),
		CreateUsernameLabel: loc.T("admin.users.create_username"),
		CreateEmailLabel:    loc.T("admin.users.create_email"),
		CreatePasswordLabel: loc.T("admin.users.create_password"),
		CreateRoleLabel:     loc.T("admin.users.create_role"),
		CreateSubmit:        loc.T("admin.users.create_submit"),
		Roles:               roleOptions(loc),

		StatusActiveLabel:   loc.T("admin.users.status.active"),
		StatusDisabledLabel: loc.T("admin.users.status.disabled"),
		EnableLabel:         loc.T("admin.users.enable"),
		DisableLabel:        loc.T("admin.users.disable"),
		ChangeRoleLabel:     loc.T("admin.users.change_role"),
		ResetPasswordLabel:  loc.T("admin.users.reset_password"),
		ForceLogoutLabel:    loc.T("admin.users.force_logout"),
		DeleteLabel:         loc.T("admin.users.delete"),
		ConfirmHint:         loc.T("admin.users.confirm_hint"),

		TempPassword:      tempPassword,
		TempPasswordLabel: loc.T("admin.users.temp_password_label"),

		PrevHref: prevHref, NextHref: nextHref,
		PrevLabel: loc.T("admin.users.prev"), NextLabel: loc.T("admin.users.next"),
		EmptyLabel: loc.T("admin.users.empty"),
	}))
}

// usersPageHref 拼分页链接，保留搜索词。
func usersPageHref(query string, page int) string {
	href := "/admin/users?page=" + strconv.Itoa(page)
	if query != "" {
		href += "&q=" + url.QueryEscape(query)
	}
	return href
}

// requireConfirm 校验危险动作的确认字段；缺失时重定向并返回 false。
func (s *Server) requireConfirm(c *gin.Context, loc *i18n.Localizer) bool {
	if c.PostForm("confirm") == "1" {
		return true
	}
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=confirm_required")
	return false
}

// loadTargetUser 按 :id 取目标用户；解析失败或不存在时返回 nil（并由调用方重定向）。
func (s *Server) loadTargetUser(c *gin.Context) *store.User {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.users.ByID(c.Request.Context(), id)
	if err != nil {
		return nil
	}
	return u
}

// wouldRemoveLastAdmin 判断对 target 做「禁用 / 删除 / 降权」是否会清空管理员。
// 仅在 target 本身是活跃管理员、且系统里没有第二个活跃管理员时为 true。
func (s *Server) wouldRemoveLastAdmin(c *gin.Context, target *store.User) bool {
	if target.Role != store.RoleAdmin || target.Status != store.StatusActive {
		return false
	}
	n, err := s.users.CountActiveAdmins(c.Request.Context())
	if err != nil {
		s.logger.Error("admin: count active admins failed", "error", err)
		return true // 读不出计数时保守拒绝，避免误锁死系统
	}
	return n <= 1
}

// adminUserCreate 新建本地账号（仅管理员）。
func (s *Server) adminUserCreate(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	username := strings.TrimSpace(c.PostForm("username"))
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	display := strings.TrimSpace(c.PostForm("display_name"))
	password := c.PostForm("password")
	role := c.PostForm("role")
	if role != store.RoleAdmin && role != store.RoleUser {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_role")
		return
	}
	if msg := validateRegisterInput(loc, username, email, password); msg != "" {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=create_failed")
		return
	}
	created, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: username, Email: email, DisplayName: display, Password: password,
		Role: role, Locale: loc.Locale(),
	})
	if err != nil {
		s.logger.Error("admin: create user failed", "username", username, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=create_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionUserCreate,
		TargetType: "user", TargetID: store.Ptr(created.ID),
		Detail: map[string]any{"username": created.Username, "role": created.Role, "by_admin": true},
	})
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=created")
}

// adminUserStatus 启用或禁用账号；禁用会作废其全部会话（M6-2 验收）。
func (s *Server) adminUserStatus(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	target := s.loadTargetUser(c)
	if target == nil {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_user")
		return
	}
	ctx := c.Request.Context()
	action := strings.TrimSpace(c.PostForm("action"))
	if action == "disable" {
		if target.ID == actor.ID {
			c.Redirect(http.StatusSeeOther, "/admin/users?notice=self_forbidden")
			return
		}
		if s.wouldRemoveLastAdmin(c, target) {
			c.Redirect(http.StatusSeeOther, "/admin/users?notice=last_admin")
			return
		}
		if err := s.accounts.DisableUser(ctx, target.ID); err != nil {
			s.logger.Error("admin: disable user failed", "user_id", target.ID, "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/users?notice=save_failed")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserDisable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
	} else {
		if err := s.accounts.EnableUser(ctx, target.ID); err != nil {
			s.logger.Error("admin: enable user failed", "user_id", target.ID, "error", err)
			c.Redirect(http.StatusSeeOther, "/admin/users?notice=save_failed")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserEnable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
	}
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=updated")
}

// adminUserRole 改角色（危险动作，需确认）；不得降掉最后一个管理员。
func (s *Server) adminUserRole(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	if !s.requireConfirm(c, loc) {
		return
	}
	role := strings.TrimSpace(c.PostForm("role"))
	if role != store.RoleAdmin && role != store.RoleUser {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_role")
		return
	}
	target := s.loadTargetUser(c)
	if target == nil {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_user")
		return
	}
	if role != store.RoleAdmin && s.wouldRemoveLastAdmin(c, target) {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=last_admin")
		return
	}
	ctx := c.Request.Context()
	if err := s.users.SetRole(ctx, target.ID, role); err != nil {
		s.logger.Error("admin: set role failed", "user_id", target.ID, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserRoleChange,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"from": target.Role, "to": role},
	})
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=updated")
}

// adminUserResetPassword 生成临时口令并作废目标用户全部会话，把口令一次性展示在页面上。
// 明文不写日志、不入库。需确认（危险动作）。
func (s *Server) adminUserResetPassword(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	if !s.requireConfirm(c, loc) {
		return
	}
	target := s.loadTargetUser(c)
	if target == nil {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_user")
		return
	}
	ctx := c.Request.Context()
	temp, err := s.accounts.ResetPassword(ctx, target.ID)
	if err != nil {
		s.logger.Error("admin: reset password failed", "user_id", target.ID, "error", err)
		s.renderUsersPage(c, loc, s.usersNotice(loc, "save_failed"), "", strings.TrimSpace(c.Query("q")), 1)
		return
	}
	// 审计只记动作与目标，绝不包含临时口令。
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserPasswordReset,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	s.renderUsersPage(c, loc, s.usersNotice(loc, "updated"), temp, strings.TrimSpace(c.Query("q")), 1)
}

// adminUserForceLogout 强制下线：作废目标用户全部会话。
func (s *Server) adminUserForceLogout(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	target := s.loadTargetUser(c)
	if target == nil {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_user")
		return
	}
	ctx := c.Request.Context()
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("admin: force logout failed", "user_id", target.ID, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserForceLogout,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=updated")
}

// adminUserDelete 删除用户：需确认；不能删自己，也不能删掉最后一个管理员。
func (s *Server) adminUserDelete(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	if !s.requireConfirm(c, loc) {
		return
	}
	target := s.loadTargetUser(c)
	if target == nil {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=invalid_user")
		return
	}
	if target.ID == actor.ID {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=self_forbidden")
		return
	}
	if s.wouldRemoveLastAdmin(c, target) {
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=last_admin")
		return
	}
	ctx := c.Request.Context()
	// 删除前先作废会话，保证即便删除中途失败也不留下可用会话。
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("admin: revoke sessions before delete failed", "user_id", target.ID, "error", err)
	}
	if err := s.users.DeleteUser(ctx, target.ID); err != nil {
		s.logger.Error("admin: delete user failed", "user_id", target.ID, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/users?notice=save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserDelete,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"username": target.Username},
	})
	c.Redirect(http.StatusSeeOther, "/admin/users?notice=deleted")
}
