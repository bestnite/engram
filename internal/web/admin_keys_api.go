package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「API Key 总览」的 SPA JSON 端点（ROADMAP.md M6-9）。
//
// 安全底线与 SSR 相同：只暴露元信息（名称、前缀、scopes、最后使用、过期、状态）；库里只存
// sha256，模型里没有明文列，因此响应不可能带出任何 key 内容。撤销是写操作，写审计且审计详情
// 不含任何 key 内容。列表分页，绝不一次拉全表。

// adminAPIKey 是一把 key 的元信息；时间已按当前管理员时区格式化，null 表示未设置。
type adminAPIKey struct {
	ID         uint64   `json:"id"`
	UserID     uint64   `json:"user_id"`
	Owner      string   `json:"owner"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
	State      string   `json:"state"`
}

// adminAPIKeysResponse 是 API Key 总览的分页响应。
type adminAPIKeysResponse struct {
	Keys  []adminAPIKey `json:"keys"`
	Page  int           `json:"page"`
	Pages int           `json:"pages"`
	Total int64         `json:"total"`
}

// adminAPIKeys 返回全用户 API Key 元信息（分页），口径与 adminAPIKeysPage 一致。
func (s *Server) adminAPIKeys(c *gin.Context) {
	ctx := c.Request.Context()
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)
	page := parsePage(c.Query("page"))

	keys, total, err := store.NewAPIKeyStore(s.db).ListAll(ctx, adminKeysPageSize, (page-1)*adminKeysPageSize)
	if err != nil {
		s.logger.Error("spa admin: list api keys failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
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
	rows := make([]adminAPIKey, 0, len(keys))
	for i := range keys {
		k := keys[i]
		row := adminAPIKey{
			ID: k.ID, UserID: k.UserID, Owner: names[k.UserID],
			Name: k.Name, Prefix: k.Prefix, Scopes: store.ParseScopes(k.Scopes),
			State: store.APIKeyState(&k, now),
		}
		if k.LastUsedAt != nil {
			v := k.LastUsedAt.In(userLoc).Format("2006-01-02 15:04")
			row.LastUsedAt = &v
		}
		if k.ExpiresAt != nil {
			v := k.ExpiresAt.In(userLoc).Format("2006-01-02")
			row.ExpiresAt = &v
		}
		rows = append(rows, row)
	}

	pages := int((total + int64(adminKeysPageSize) - 1) / int64(adminKeysPageSize))
	if pages < 1 {
		pages = 1
	}
	c.JSON(http.StatusOK, adminAPIKeysResponse{Keys: rows, Page: page, Pages: pages, Total: total})
}

// adminAPIKeyRevoke 撤销任意用户的一把 key（管理员动作）；幂等。
func (s *Server) adminAPIKeyRevoke(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		adminError(c, http.StatusNotFound, "invalid_key")
		return
	}
	ctx := c.Request.Context()
	if err := store.NewAPIKeyStore(s.db).RevokeByID(ctx, id, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			adminError(c, http.StatusNotFound, "invalid_key")
			return
		}
		s.logger.Error("spa admin: revoke api key failed", "key_id", id, "error", err)
		adminError(c, http.StatusInternalServerError, "revoke_failed")
		return
	}
	// 审计只记「谁撤销了哪把 key」；不写任何 key 内容（明文不存在，哈希也不该进审计）。
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAPIKeyRevoke,
		TargetType: "api_key", TargetID: store.Ptr(id),
	})
	c.Status(http.StatusNoContent)
}
