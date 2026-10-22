package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "API Key overview".
//
// The security baseline is the same as SSR: only metadata is exposed (name, prefix, scopes, last used, expiry, status); the DB stores only
// sha256, there is no plaintext column in the model, so the response can never carry any key content. Revocation is a write operation: it writes an audit row, and the audit detail
// contains no key content. The list is paged and never pulls the whole table at once.

// adminAPIKey is the metadata of one key; times are already formatted in the current admin's timezone, null means not set.
type adminAPIKey struct {
	ID         string   `json:"id"`
	UserID     string   `json:"user_id"`
	Owner      string   `json:"owner"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
	State      string   `json:"state"`
}

// adminAPIKeysResponse is the paged response for the API Key overview.
type adminAPIKeysResponse struct {
	Keys  []adminAPIKey `json:"keys"`
	Page  int           `json:"page"`
	Pages int           `json:"pages"`
	Total int64         `json:"total"`
}

// adminAPIKeys returns API Key metadata for all users, paged in adminKeysPageSize rows per page.
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

	// Batch-fetch the owning users, resolving only the user_ids appearing on the current page (at most one page).
	owners := make(map[uint64]*store.User)
	if s.users != nil {
		for i := range keys {
			uid := keys[i].UserID
			if _, seen := owners[uid]; seen {
				continue
			}
			if u, err := s.users.ByID(ctx, uid); err == nil {
				owners[uid] = u
			}
		}
	}

	now := time.Now().UTC()
	rows := make([]adminAPIKey, 0, len(keys))
	for i := range keys {
		k := keys[i]
		owner := owners[k.UserID]
		row := adminAPIKey{
			ID: k.PublicID, Name: k.Name, Prefix: k.Prefix, Scopes: store.ParseScopes(k.Scopes),
			State: store.APIKeyState(&k, now),
		}
		if owner != nil {
			row.UserID = owner.PublicID
			row.Owner = owner.Username
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

// adminAPIKeyRevoke revokes one of any user's keys (an admin action); idempotent.
func (s *Server) adminAPIKeyRevoke(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		adminError(c, http.StatusNotFound, "invalid_key")
		return
	}
	ctx := c.Request.Context()
	keyStore := store.NewAPIKeyStore(s.db)
	key, err := keyStore.ByPublicID(ctx, raw)
	if err != nil {
		adminError(c, http.StatusNotFound, "invalid_key")
		return
	}
	if err := keyStore.RevokeByID(ctx, key.ID, time.Now().UTC()); err != nil {
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			adminError(c, http.StatusNotFound, "invalid_key")
			return
		}
		s.logger.Error("spa admin: revoke api key failed", "key_id", key.ID, "error", err)
		adminError(c, http.StatusInternalServerError, "revoke_failed")
		return
	}
	// The audit records only "who revoked which key"; no key content is written (plaintext does not exist, and the hash should not enter the audit either).
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(u.ID), Action: store.ActionAPIKeyRevoke,
		TargetType: "api_key", TargetID: store.Ptr(key.ID),
	})
	c.Status(http.StatusNoContent)
}
