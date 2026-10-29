package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// listKeys lists metadata of the current user's own API keys (no hashes, no plaintext), scope: admin.
func (a *API) listKeys(c *gin.Context) {
	u, _ := CurrentUser(c)
	keys, err := a.keys.ListByUser(c.Request.Context(), u.ID)
	if err != nil {
		a.logger.Error("list api keys failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// createKeyRequest is the create-API-key request body; when scopes is empty the server falls back to the default read.
type createKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// createKey creates a key; the plaintext appears exactly once, in this response.
func (a *API) createKey(c *gin.Context) {
	u, ok := CurrentUser(c)
	if !ok || u == nil {
		abortError(c, http.StatusUnauthorized, CodeUnauthorized, "")
		return
	}
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	// An expiry that is not strictly in the future would create a key that is already dead, so it is rejected
	// before anything is persisted. This is a request-shape check and therefore belongs to the transport layer.
	if req.ExpiresAt != nil && !req.ExpiresAt.After(a.now()) {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	// admin scope can only be issued to admin accounts: non-admins (including ordinary users holding keys or a leftover admin-scope key)
	// are rejected on any path that submits admin, and nothing is persisted. The restriction has to happen in the transport layer, which knows
	// the actor's role; the store layer cannot see the caller's identity.
	if store.ScopesIncludeAdmin(req.Scopes) && u.Role != store.RoleAdmin {
		abortError(c, http.StatusForbidden, CodeScopeNotGrantable, "")
		return
	}
	created, err := a.keys.Create(c.Request.Context(), store.CreateAPIKeyParams{
		UserID:    u.ID,
		Name:      req.Name,
		Scopes:    req.Scopes,
		ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	a.auditor.Record(c.Request.Context(), store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: CurrentAPIKeyID(c),
		Action:   store.ActionAPIKeyCreate,
		Detail:   map[string]any{"key_id": created.Key.ID, "name": created.Key.Name, "scopes": created.Key.Scopes},
	})
	c.JSON(http.StatusCreated, gin.H{"key": created.Key, "plaintext": created.Plaintext})
}

// deleteKey revokes a key belonging to the current user; revocation takes effect immediately.
func (a *API) deleteKey(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	k, err := a.keys.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	if err := a.keys.Revoke(ctx, u.ID, k.ID, a.now()); err != nil {
		if err == store.ErrAPIKeyNotFound {
			abortNotFound(c)
			return
		}
		a.logger.Error("revoke api key failed", "key_id", k.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "")
		return
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: CurrentAPIKeyID(c),
		Action:   "api_key.revoke",
		Detail:   map[string]any{"key_id": k.ID},
	})
	// Echo the external id, never the auto-increment primary key.
	c.JSON(http.StatusOK, gin.H{"revoked": true, "id": publicID})
}
