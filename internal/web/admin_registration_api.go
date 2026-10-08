package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「注册与邀请」的 SPA JSON 端点（ROADMAP.md M6-3）。
//
// 免重启生效的关键与 SSR 相同：settings 表按请求现读（注册流程每次 LoadSettings），因此写库后
// 下一次注册尝试立即按新策略判定。校验与写库调用与 SSR 表单完全同一份 store/auth 函数。

// adminInvite 是一条邀请的原始字段；role/status 是存储取值，由前端映射文案。
type adminInvite struct {
	ID        uint64 `json:"id"`
	Token     string `json:"token"`
	Link      string `json:"link"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	UsedAt    string `json:"used_at"`
	UsedBy    string `json:"used_by"`
}

// adminRegistrationResponse 是注册与邀请页的读取结果。
type adminRegistrationResponse struct {
	Policy       string        `json:"policy"`
	EmailDomains string        `json:"email_domains"`
	Invites      []adminInvite `json:"invites"`
}

// adminRegistrationRequest 是保存注册策略与白名单的请求体。
type adminRegistrationRequest struct {
	Policy       string `json:"policy"`
	EmailDomains string `json:"email_domains"`
}

// adminInviteCreateRequest 是创建邀请的请求体。
// ExpiresDays 为 nil 或 0 表示不过期；负数拒绝。
type adminInviteCreateRequest struct {
	Email       string `json:"email"`
	Role        string `json:"role"`
	ExpiresDays *int   `json:"expires_days"`
	SendEmail   bool   `json:"send_email"`
}

// adminInviteCreateResponse 回带新邀请与发信结果码（空串表示未请求或不适用）。
type adminInviteCreateResponse struct {
	Invite     adminInvite `json:"invite"`
	MailNotice string      `json:"mail_notice"`
}

// adminInviteJSON 把一条邀请转换成响应结构；used_by 解析成用户名，解析失败回落到十进制 id。
func (s *Server) adminInviteJSON(c *gin.Context, inv store.Invite, now time.Time) adminInvite {
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
	expires := ""
	if inv.ExpiresAt != nil {
		expires = inv.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	return adminInvite{
		ID:        inv.ID,
		Token:     inv.Token,
		Link:      "/register?invite=" + inv.Token,
		Email:     email,
		Role:      inv.Role,
		Status:    inviteStatus(inv, now),
		CreatedAt: inv.CreatedAt.UTC().Format("2006-01-02 15:04"),
		ExpiresAt: expires,
		UsedAt:    formatTimePtr(inv.UsedAt),
		UsedBy:    usedBy,
	}
}

// adminRegistration 返回当前注册策略、白名单与邀请列表。
func (s *Server) adminRegistration(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := store.LoadSettings(ctx, s.db)
	if err != nil {
		s.logger.Error("spa admin: load settings failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	resp := adminRegistrationResponse{
		Policy:       auth.ParseRegistrationPolicy(settings[auth.SettingKeyRegistrationPolicy]),
		EmailDomains: strings.TrimSpace(settings[auth.SettingKeyEmailAllowlist]),
		Invites:      []adminInvite{},
	}
	if s.invites != nil {
		invites, err := s.invites.List(ctx)
		if err != nil {
			s.logger.Error("spa admin: list invites failed", "error", err)
			adminError(c, http.StatusInternalServerError, "internal_error")
			return
		}
		now := time.Now().UTC()
		for _, inv := range invites {
			resp.Invites = append(resp.Invites, s.adminInviteJSON(c, inv, now))
		}
	}
	c.JSON(http.StatusOK, resp)
}

// adminRegistrationSave 写入注册策略与邮箱域名白名单（均为普通 settings），写审计。
func (s *Server) adminRegistrationSave(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	policy := strings.TrimSpace(req.Policy)
	switch policy {
	case auth.PolicyOpen, auth.PolicyInvite, auth.PolicyClosed:
	default:
		adminError(c, http.StatusBadRequest, "invalid_policy")
		return
	}
	// 白名单归一化后以逗号分隔存储（ParseEmailAllowlist 兼容该写法）；空串表示不限制。
	allowlist := strings.Join(auth.ParseEmailAllowlist(req.EmailDomains), ",")
	ctx := c.Request.Context()
	now := time.Now().UTC()

	if err := store.PutSetting(ctx, s.db, auth.SettingKeyRegistrationPolicy, policy, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("spa admin: save registration policy failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	if err := store.PutSetting(ctx, s.db, auth.SettingKeyEmailAllowlist, allowlist, store.Ptr(actor.ID), now); err != nil {
		s.logger.Error("spa admin: save email allowlist failed", "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionSettingUpdate,
		TargetType: "setting",
		Detail:     map[string]any{"keys": []string{auth.SettingKeyRegistrationPolicy, auth.SettingKeyEmailAllowlist}, "policy": policy},
	})
	c.Status(http.StatusNoContent)
}

// adminInviteCreate 创建一条邀请：可选限定邮箱、角色与有效天数，可选寄信。
// 发信的任何问题都不影响创建结果——只通过 mail_notice 如实回显发生了什么。
func (s *Server) adminInviteCreate(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	if s.invites == nil {
		adminError(c, http.StatusInternalServerError, "invite_create_failed")
		return
	}
	var req adminInviteCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	role := strings.TrimSpace(req.Role)
	if role != store.RoleAdmin && role != store.RoleUser {
		adminError(c, http.StatusBadRequest, "invite_create_failed")
		return
	}
	inv := &store.Invite{Role: role, CreatedBy: store.Ptr(actor.ID), CreatedAt: time.Now().UTC()}
	if email := strings.ToLower(strings.TrimSpace(req.Email)); email != "" {
		inv.Email = &email
	}
	// 有效天数：留空或 0 表示不过期；非法值拒绝而不是静默当成不过期。
	if req.ExpiresDays != nil {
		if *req.ExpiresDays < 0 {
			adminError(c, http.StatusBadRequest, "invite_create_failed")
			return
		}
		if *req.ExpiresDays > 0 {
			exp := time.Now().UTC().AddDate(0, 0, *req.ExpiresDays)
			inv.ExpiresAt = &exp
		}
	}
	ctx := c.Request.Context()
	if err := s.invites.Create(ctx, inv); err != nil {
		s.logger.Error("spa admin: create invite failed", "error", err)
		adminError(c, http.StatusInternalServerError, "invite_create_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteCreate,
		TargetType: "invite", TargetID: store.Ptr(inv.ID),
		// 审计不记录 token 明文（等价于凭据）。
		Detail: map[string]any{"role": inv.Role, "has_email": inv.Email != nil, "expires": inv.ExpiresAt != nil},
	})
	mailNotice := ""
	if req.SendEmail {
		loc, ok := s.localizer(c)
		if !ok {
			return
		}
		if code := s.sendInviteEmail(c, loc, inv); code != "" {
			mailNotice = code
		}
	}
	c.JSON(http.StatusCreated, adminInviteCreateResponse{
		Invite:     s.adminInviteJSON(c, *inv, time.Now().UTC()),
		MailNotice: mailNotice,
	})
}

// adminInviteRevoke 撤销一条邀请（删除整行），写审计。
func (s *Server) adminInviteRevoke(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 || s.invites == nil {
		adminError(c, http.StatusNotFound, "invite_invalid")
		return
	}
	ctx := c.Request.Context()
	if err := s.invites.Revoke(ctx, id); err != nil {
		if errors.Is(err, store.ErrInviteNotFound) {
			adminError(c, http.StatusNotFound, "invite_invalid")
			return
		}
		s.logger.Error("spa admin: revoke invite failed", "invite_id", id, "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionInviteRevoke,
		TargetType: "invite", TargetID: store.Ptr(id),
	})
	c.Status(http.StatusNoContent)
}
