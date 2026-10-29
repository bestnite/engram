package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "user management".
//
// Semantics match the SSR forms (admin_users.go) item by item, only the transport shape changes: the same store/service calls,
// the same dangerous-action protection (cannot delete yourself, cannot wipe out the last admin, dangerous actions need confirmation). Responses carry only raw
// values and stable English codes; localized copy is mapped by the frontend language packs by code.

// adminUser is one row in the user list; role/status are stored values whose copy is mapped by the frontend.
type adminUser struct {
	ID          string `json:"id"`
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

// adminUsersResponse is the user list response; query echoes the search term so paging links keep the filter.
type adminUsersResponse struct {
	Users []adminUser `json:"users"`
	Page  int         `json:"page"`
	Pages int         `json:"pages"`
	Total int64       `json:"total"`
	Query string      `json:"query"`
}

// adminUserCreateRequest is the request body for creating a local account.
type adminUserCreateRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

// adminUserStatusRequest is the enable/disable request body; action is enable|disable.
type adminUserStatusRequest struct {
	Action string `json:"action"`
}

// adminUserRoleRequest is the role-change request body; the dangerous action needs confirm.
type adminUserRoleRequest struct {
	Role    string `json:"role"`
	Confirm bool   `json:"confirm"`
}

// adminUserConfirmRequest is the request body for dangerous actions that only need confirmation (reset password / delete).
type adminUserConfirmRequest struct {
	Confirm bool `json:"confirm"`
}

// adminUserPasswordResponse returns the temporary password after a reset, once (never logged, never stored).
type adminUserPasswordResponse struct {
	TempPassword string `json:"temp_password"`
}

// adminUsers returns the user list: search, paging in store.AdminListUsersPageSize rows and per-user usage.
func (s *Server) adminUsers(c *gin.Context) {
	ctx := c.Request.Context()
	query := strings.TrimSpace(c.Query("q"))
	page := parsePage(c.Query("page"))
	size := store.AdminListUsersPageSize

	users, total, err := s.users.ListForAdmin(ctx, query, page, size)
	if err != nil {
		s.logger.Error("spa admin: list users failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	var currentID uint64
	if u, ok := auth.CurrentUser(c); ok {
		currentID = u.ID
	}
	rows := make([]adminUser, 0, len(users))
	for _, u := range users {
		usage, err := s.users.UsageCounts(ctx, u.ID)
		if err != nil {
			s.logger.Error("spa admin: usage counts failed", "user_id", u.ID, "error", err)
		}
		rows = append(rows, adminUser{
			ID: u.PublicID, Username: u.Username, Email: u.Email, DisplayName: u.DisplayName,
			Role: u.Role, Status: u.Status,
			Decks: usage.Decks, Cards: usage.Cards, Reviews: usage.Reviews,
			IsSelf: u.ID == currentID,
		})
	}
	pages := int((total + int64(size) - 1) / int64(size))
	if pages < 1 {
		pages = 1
	}
	c.JSON(http.StatusOK, adminUsersResponse{Users: rows, Page: page, Pages: pages, Total: total, Query: query})
}

// adminUserCreate creates a local account (admins only). Validation reuses registerInputErrorCode,
// and on failure hands a stable code to the frontend to map, never writing to the DB.
func (s *Server) adminUserCreate(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminUserCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Role != store.RoleAdmin && req.Role != store.RoleUser {
		adminError(c, http.StatusBadRequest, "invalid_role")
		return
	}
	if code := registerInputErrorCode(req.Username, req.Email, req.Password); code != "" {
		adminError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	created, err := s.accounts.CreateLocalUser(ctx, auth.CreateUserInput{
		Username: req.Username, Email: req.Email, DisplayName: req.DisplayName,
		Password: req.Password, Role: req.Role, Locale: requestLocale(c),
	})
	if err != nil {
		s.logger.Error("spa admin: create user failed", "username", req.Username, "error", err)
		adminError(c, http.StatusBadRequest, "create_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserCreate,
		TargetType: "user", TargetID: store.Ptr(created.ID),
		Detail: map[string]any{"username": created.Username, "role": created.Role, "by_admin": true},
	})
	c.JSON(http.StatusCreated, adminUser{
		ID: created.PublicID, Username: created.Username, Email: created.Email,
		DisplayName: created.DisplayName, Role: created.Role, Status: created.Status,
	})
}

// adminUserStatus enables or disables an account; disabling invalidates all of its sessions
func (s *Server) adminUserStatus(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	target, ok := s.adminTargetUser(c)
	if !ok {
		return
	}
	var req adminUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	if strings.TrimSpace(req.Action) == "disable" {
		if target.ID == actor.ID {
			adminError(c, http.StatusBadRequest, "self_forbidden")
			return
		}
		// The decision and the write are in the same gate transaction: when two admins disable each other simultaneously only one can succeed.
		err := store.WithAdminGate(ctx, s.db, func(tx *gorm.DB, admins int64) error {
			return guardedUserUpdate(tx, target.ID, admins, "status", store.StatusDisabled)
		})
		if s.writeAdminGateError(c, err, "disable user", target.ID) {
			return
		}
		// The status has already been written inside the gate; invalidating sessions and refreshing state are done by the account service (the status write is idempotent).
		if err := s.accounts.DisableUser(ctx, target.ID); err != nil {
			s.logger.Error("spa admin: disable user failed", "user_id", target.ID, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		s.auditor.Record(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserDisable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
		s.notifyAccountStatus(ctx, target, "disabled")
	} else {
		if err := s.accounts.EnableUser(ctx, target.ID); err != nil {
			s.logger.Error("spa admin: enable user failed", "user_id", target.ID, "error", err)
			adminError(c, http.StatusInternalServerError, "save_failed")
			return
		}
		s.auditor.Record(ctx, store.AuditEntry{
			UserID: store.Ptr(actor.ID), Action: store.ActionUserEnable,
			TargetType: "user", TargetID: store.Ptr(target.ID),
		})
	}
	c.Status(http.StatusNoContent)
}

// adminUserRole changes the role (a dangerous action, needs confirmation); it must not demote the last admin.
func (s *Server) adminUserRole(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminUserRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if !req.Confirm {
		adminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	if req.Role != store.RoleAdmin && req.Role != store.RoleUser {
		adminError(c, http.StatusBadRequest, "invalid_role")
		return
	}
	target, ok := s.adminTargetUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	err := store.WithAdminGate(ctx, s.db, func(tx *gorm.DB, admins int64) error {
		if req.Role == store.RoleAdmin {
			return tx.Model(&store.User{}).Where("id = ?", target.ID).Update("role", req.Role).Error
		}
		return guardedUserUpdate(tx, target.ID, admins, "role", req.Role)
	})
	if s.writeAdminGateError(c, err, "set role", target.ID) {
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserRoleChange,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"from": target.Role, "to": req.Role},
	})
	c.Status(http.StatusNoContent)
}

// adminUserResetPassword generates a temporary password and invalidates all of the target user's sessions, returning the password once.
func (s *Server) adminUserResetPassword(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminUserConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		adminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	target, ok := s.adminTargetUser(c)
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
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserPasswordReset,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	c.JSON(http.StatusOK, adminUserPasswordResponse{TempPassword: temp})
}

// adminUserForceLogout forces a sign-out: invalidates all of the target user's sessions.
func (s *Server) adminUserForceLogout(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	target, ok := s.adminTargetUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("spa admin: force logout failed", "user_id", target.ID, "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserForceLogout,
		TargetType: "user", TargetID: store.Ptr(target.ID),
	})
	c.Status(http.StatusNoContent)
}

// adminUserDelete deletes a user: needs confirmation; cannot delete yourself and cannot delete the last admin.
func (s *Server) adminUserDelete(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	var req adminUserConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		adminError(c, http.StatusBadRequest, "confirm_required")
		return
	}
	target, ok := s.adminTargetUser(c)
	if !ok {
		return
	}
	if target.ID == actor.ID {
		adminError(c, http.StatusBadRequest, "self_forbidden")
		return
	}
	ctx := c.Request.Context()
	// First do a lock-free pre-check outside the gate: a request that would obviously wipe out the admins sends no notification and invalidates no sessions.
	if s.wouldRemoveLastAdmin(c, target) {
		adminError(c, http.StatusBadRequest, "last_admin")
		return
	}
	// Send the "account deleted" notification before deleting; a mail failure does not affect the deletion (the notify function returns no error).
	s.notifyAccountStatus(ctx, target, "deleted")
	// Invalidate sessions before deleting, so that even if the deletion fails midway no usable session is left behind.
	if err := s.accounts.ForceLogout(ctx, target.ID); err != nil {
		s.logger.Error("spa admin: revoke sessions before delete failed", "user_id", target.ID, "error", err)
	}
	// The authoritative decision and the deletion are in the same gate transaction.
	err := store.WithAdminGate(ctx, s.db, func(tx *gorm.DB, admins int64) error {
		var fresh store.User
		if err := tx.First(&fresh, "id = ?", target.ID).Error; err != nil {
			return err
		}
		if store.RemovesLastAdmin(&fresh, admins) {
			return store.ErrLastAdmin
		}
		return store.DeleteUserTx(ctx, tx, target.ID)
	})
	if s.writeAdminGateError(c, err, "delete user", target.ID) {
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionUserDelete,
		TargetType: "user", TargetID: store.Ptr(target.ID),
		Detail: map[string]any{"username": target.Username},
	})
	c.Status(http.StatusNoContent)
}

// adminTargetUser parses the :id public id and loads the target user; on unknown, empty string or not found it writes 404 and returns false.
func (s *Server) adminTargetUser(c *gin.Context) (*store.User, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		adminError(c, http.StatusNotFound, "invalid_user")
		return nil, false
	}
	u, err := s.users.ByPublicID(c.Request.Context(), raw)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			adminError(c, http.StatusNotFound, "invalid_user")
			return nil, false
		}
		s.logger.Error("spa admin: load target user failed", "user_id", raw, "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return nil, false
	}
	return u, true
}

// guardedUserUpdate re-reads the target user inside the gate transaction and, after confirming this "disable / demote" will not wipe out the admins,
// updates one column. The read must be redone inside the transaction: the row read outside the gate may have been changed by a concurrent request.
func guardedUserUpdate(tx *gorm.DB, userID uint64, admins int64, column string, value any) error {
	var fresh store.User
	if err := tx.First(&fresh, "id = ?", userID).Error; err != nil {
		return err
	}
	if store.RemovesLastAdmin(&fresh, admins) {
		return store.ErrLastAdmin
	}
	return tx.Model(&store.User{}).Where("id = ?", userID).Update(column, value).Error
}

// writeAdminGateError maps the gate transaction's result into a response; when there is an error it writes the response and returns true.
func (s *Server) writeAdminGateError(c *gin.Context, err error, action string, userID uint64) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrLastAdmin):
		adminError(c, http.StatusBadRequest, "last_admin")
	default:
		s.logger.Error("spa admin: "+action+" failed", "user_id", userID, "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
	}
	return true
}
