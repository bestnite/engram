package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「用户管理」的 SPA JSON 端点（DESIGN.md §8.4；ROADMAP.md M6-2）。
//
// 语义与 SSR 表单（admin_users.go）逐项一致，只换传输形态：同一份 store/service 调用、
// 同一份危险动作保护（不可删自己、不可清空最后一个管理员、危险动作需确认）。响应只带原始
// 值与稳定英文 code，本地化文案由前端语言包按 code 映射（DESIGN.md §8.3）。

// spaAdminUser 是用户列表里的一行；role/status 是存储取值，由前端映射文案。
type spaAdminUser struct {
	ID          uint64 `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Decks       int64  `json:"decks"`
	Cards       int64  `json:"cards"`
	Reviews     int64  `json:"reviews"`
	IsSelf      bool   `json:"is_self"`
}

// spaAdminUsersResponse 是用户列表响应；query 回带搜索词，供分页链接保持过滤。
type spaAdminUsersResponse struct {
	Users []spaAdminUser `json:"users"`
	Page  int            `json:"page"`
	Pages int            `json:"pages"`
	Total int64          `json:"total"`
	Query string         `json:"query"`
}

// spaAdminUserCreateRequest 是新建本地账号的请求体。
type spaAdminUserCreateRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

// spaAdminUserStatusRequest 是启用/禁用的请求体；action 取 enable|disable。
type spaAdminUserStatusRequest struct {
	Action string `json:"action"`
}

// spaAdminUserRoleRequest 是改角色的请求体；危险动作需 confirm。
type spaAdminUserRoleRequest struct {
	Role    string `json:"role"`
	Confirm bool   `json:"confirm"`
}

// spaAdminUserConfirmRequest 是仅需确认的危险动作请求体（重置密码 / 删除）。
type spaAdminUserConfirmRequest struct {
	Confirm bool `json:"confirm"`
}

// spaAdminUserPasswordResponse 一次性返回重置后的临时口令（绝不写日志、不入库）。
type spaAdminUserPasswordResponse struct {
	TempPassword string `json:"temp_password"`
}

// spaAdminUsers 返回用户列表（搜索 + 分页 + 每用户用量），口径与 renderUsersPage 一致。
func (s *Server) spaAdminUsers(c *gin.Context) {
	ctx := c.Request.Context()
	query := strings.TrimSpace(c.Query("q"))
	page := parsePage(c.Query("page"))
	size := store.AdminListUsersPageSize

	users, total, err := s.users.ListForAdmin(ctx, query, page, size)
	if err != nil {
		s.logger.Error("spa admin: list users failed", "error", err)
		spaAdminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	var currentID uint64
	if u, ok := auth.CurrentUser(c); ok {
		currentID = u.ID
	}
	rows := make([]spaAdminUser, 0, len(users))
	for _, u := range users {
		usage, err := s.users.UsageCounts(ctx, u.ID)
		if err != nil {
			s.logger.Error("spa admin: usage counts failed", "user_id", u.ID, "error", err)
		}
		rows = append(rows, spaAdminUser{
			ID: u.ID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
			Role: u.Role, Status: u.Status,
			Decks: usage.Decks, Cards: usage.Cards, Reviews: usage.Reviews,
			IsSelf: u.ID == currentID,
		})
	}
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	c.JSON(http.StatusOK, spaAdminUsersResponse{Users: rows, Page: page, Pages: pages, Total: total, Query: query})
}

// spaAdminUserCreate 新建本地账号（仅管理员）。校验复用 registerInputErrorCode，
// 失败时把稳定 code 交给前端映射，绝不落库。
func (s *Server) spaAdminUserCreate(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req spaAdminUserCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Role != store.RoleAdmin && req.Role != store.RoleUser {
		spaAdminError(c, http.StatusBadRequest, "invalid_role")
		return
	}
	if code := registerInputErrorCode(req.Username, req.Email, req.Password); code != "" {
		spaAdminError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	created, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: req.Username, Email: req.Email, DisplayName: req.DisplayName,
		Password: req.Password, Role: req.Role, Locale: requestLocale(c),
	})
	if err != nil {
		s.logger.Error("spa admin: create user failed", "username", req.Username, "error", err)
		spaAdminError(c, http.StatusBadRequest, "create_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserCreate,
		TargetType: "user", TargetID: store.Ptr(created.ID),
		Detail: map[string]any{"username": created.Username, "role": created.Role, "by_admin": true},
	})
	c.JSON(http.StatusCreated, spaAdminUser{
		ID: created.ID, Username: created.Username, Email: created.Email,
		DisplayName: created.DisplayName, Role: created.Role, Status: created.Status,
	})
}

// spaAdminUserStatus 启用或禁用账号；禁用会作废其全部会话（M6-2 验收）。
func (s *Server) spaAdminUserStatus(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	target, ok := s.spaAdminTargetUser(c)
	if !ok {
		return
	}
	var req spaAdminUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	if strings.TrimSpace(req.Action) == "disable" {
		if target.ID == actor.ID {
			spaAdminError(c, http.StatusBadRequest, "self_forbidden")
			return
		}
		if s.wouldRemoveLastAdmin(c, target) {
			spaAdminError(c, http.StatusBadRequest, "last_admin")
			return
		}
		if err := s.accounts.DisableUser(ctx, target.ID); err != nil {
			s.logger.Error("spa admin: disable user failed", "user_id", target.ID, "error", err)
			spaAdminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserDisable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
		s.notifyAccountStatus(ctx, target, "disabled")
	} else {
		if err := s.accounts.EnableUser(ctx, target.ID); err != nil {
			s.logger.Error("spa admin: enable user failed", "user_id", target.ID, "error", err)
			spaAdminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserEnable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
	}
	c.Status(http.StatusNoContent)
}

// spaAdminUserRole 改角色（危险动作，需确认）；不得降掉最后一个管理员。
func (s *Server) spaAdminUserRole(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req spaAdminUserRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if !req.Confirm {
		spaAdminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	if req.Role != store.RoleAdmin && req.Role != store.RoleUser {
		spaAdminError(c, http.StatusBadRequest, "invalid_role")
		return
	}
	target, ok := s.spaAdminTargetUser(c)
	if !ok {
		return
	}
	if req.Role != store.RoleAdmin && s.wouldRemoveLastAdmin(c, target) {
		spaAdminError(c, http.StatusBadRequest, "last_admin")
		return
	}
	ctx := c.Request.Context()
	if err := s.users.SetRole(ctx, target.ID, req.Role); err != nil {
		s.logger.Error("spa admin: set role failed", "user_id", target.ID, "error", err)
		spaAdminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserRoleChange,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"from": target.Role, "to": req.Role},
	})
	c.Status(http.StatusNoContent)
}

// spaAdminUserResetPassword 生成临时口令并作废目标用户全部会话，一次性返回口令。
func (s *Server) spaAdminUserResetPassword(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req spaAdminUserConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		spaAdminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	target, ok := s.spaAdminTargetUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var temp string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var txErr error
		temp, txErr = s.accounts.ResetPasswordTx(ctx, tx, target.ID)
		return txErr
	})
	if err != nil {
		s.logger.Error("spa admin: reset password failed", "user_id", target.ID, "error", err)
		spaAdminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserPasswordReset,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	c.JSON(http.StatusOK, spaAdminUserPasswordResponse{TempPassword: temp})
}

// spaAdminUserForceLogout 强制下线：作废目标用户全部会话。
func (s *Server) spaAdminUserForceLogout(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	target, ok := s.spaAdminTargetUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("spa admin: force logout failed", "user_id", target.ID, "error", err)
		spaAdminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserForceLogout,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	c.Status(http.StatusNoContent)
}

// spaAdminUserDelete 删除用户：需确认；不能删自己，也不能删掉最后一个管理员。
func (s *Server) spaAdminUserDelete(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req spaAdminUserConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		spaAdminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	target, ok := s.spaAdminTargetUser(c)
	if !ok {
		return
	}
	if target.ID == actor.ID {
		spaAdminError(c, http.StatusBadRequest, "self_forbidden")
		return
	}
	if s.wouldRemoveLastAdmin(c, target) {
		spaAdminError(c, http.StatusBadRequest, "last_admin")
		return
	}
	ctx := c.Request.Context()
	// M1-19：删除前先发「账号被删除」通知；发信失败不影响删除（通知函数不返回 error）。
	s.notifyAccountStatus(ctx, target, "deleted")
	// 删除前先作废会话，保证即便删除中途失败也不留下可用会话。
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("spa admin: revoke sessions before delete failed", "user_id", target.ID, "error", err)
	}
	if err := s.users.DeleteUser(ctx, target.ID); err != nil {
		s.logger.Error("spa admin: delete user failed", "user_id", target.ID, "error", err)
		spaAdminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserDelete,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"username": target.Username},
	})
	c.Status(http.StatusNoContent)
}

// spaAdminTargetUser 解析 :id 并取目标用户；解析失败或不存在时写 404 并返回 false。
func (s *Server) spaAdminTargetUser(c *gin.Context) (*store.User, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		spaAdminError(c, http.StatusNotFound, "invalid_user")
		return nil, false
	}
	u, err := s.users.ByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			spaAdminError(c, http.StatusNotFound, "invalid_user")
			return nil, false
		}
		s.logger.Error("spa admin: load target user failed", "user_id", id, "error", err)
		spaAdminError(c, http.StatusInternalServerError, "internal_error")
		return nil, false
	}
	return u, true
}
