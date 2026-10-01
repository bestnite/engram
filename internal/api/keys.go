package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

// listKeys 列出当前用户自己的 API key 元信息（不含哈希与明文），scope: admin。
func (a *API) listKeys(c *gin.Context) {
	u, _ := CurrentUser(c)
	keys, err := a.keys.ListByUser(c.Request.Context(), u.ID)
	if err != nil {
		a.logger.Error("list api keys failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to list api keys")
		return
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// createKeyRequest 是创建 API key 的请求体；scopes 为空时服务端落到默认 read。
type createKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// createKey 新建一个 key；明文只在这次响应里出现一次（DESIGN.md §7.2）。
func (a *API) createKey(c *gin.Context) {
	u, _ := CurrentUser(c)
	var req createKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "request body must be valid JSON")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "name is required")
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
	a.audit(c.Request.Context(), store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: CurrentAPIKeyID(c),
		Action:   "api_key.create",
		Detail:   map[string]any{"key_id": created.Key.ID, "name": created.Key.Name, "scopes": created.Key.Scopes},
	})
	c.JSON(http.StatusCreated, gin.H{"key": created.Key, "plaintext": created.Plaintext})
}

// deleteKey 撤销一个属于当前用户的 key；撤销即时生效（DESIGN.md §7.2）。
func (a *API) deleteKey(c *gin.Context) {
	u, _ := CurrentUser(c)
	keyID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := a.keys.Revoke(c.Request.Context(), u.ID, keyID, a.now()); err != nil {
		if err == store.ErrAPIKeyNotFound {
			abortError(c, http.StatusNotFound, CodeNotFound, "api key not found")
			return
		}
		a.logger.Error("revoke api key failed", "key_id", keyID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to revoke api key")
		return
	}
	a.audit(c.Request.Context(), store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: CurrentAPIKeyID(c),
		Action:   "api_key.revoke",
		Detail:   map[string]any{"key_id": keyID},
	})
	c.JSON(http.StatusOK, gin.H{"revoked": true, "id": keyID})
}
