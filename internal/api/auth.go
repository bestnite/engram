package api

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 上下文键用未导出类型，避免与其它包冲突。
type ctxKey string

const (
	ctxUserKey ctxKey = "api.user"
	ctxKeyKey  ctxKey = "api.api_key"
)

// AuthConfig 是鉴权中间件的显式配置；缺少依赖时构造失败。
type AuthConfig struct {
	Keys    *store.APIKeyStore
	Users   *store.UserStore
	Auditor *auth.Auditor
	Logger  *slog.Logger
	// Now 可注入时钟，便于测试过期与限流窗口。
	Now func() time.Time
	// ReadLimit / WriteLimit / RateWindow 见 RateLimiter。
	ReadLimit  int
	WriteLimit int
	RateWindow time.Duration
}

// Authenticator 实现 M4-2：同一条请求管道同时接受会话 cookie 与 bearer key，
// 两者都解析到同一个用户，随后由 RequireScope 强制 scope。
type Authenticator struct {
	keys    *store.APIKeyStore
	users   *store.UserStore
	auditor *auth.Auditor
	logger  *slog.Logger
	now     func() time.Time
	read    *RateLimiter
	write   *RateLimiter
}

// NewAuthenticator 构造鉴权中间件；Keys / Users 必填。
func NewAuthenticator(cfg AuthConfig) (*Authenticator, error) {
	if cfg.Keys == nil || cfg.Users == nil {
		return nil, errors.New("api: api key store and user store are required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	readLimit := cfg.ReadLimit
	if readLimit <= 0 {
		readLimit = DefaultReadLimit
	}
	writeLimit := cfg.WriteLimit
	if writeLimit <= 0 {
		writeLimit = DefaultWriteLimit
	}
	return &Authenticator{
		keys:    cfg.Keys,
		users:   cfg.Users,
		auditor: cfg.Auditor,
		logger:  logger,
		now:     now,
		read:    NewRateLimiter(readLimit, cfg.RateWindow, now),
		write:   NewRateLimiter(writeLimit, cfg.RateWindow, now),
	}, nil
}

// Auth 是 /api/v1 的入口中间件：优先看 bearer key，其次回落到会话 cookie。
//
// 会话通道依赖全局会话中间件（web.New 装配）已把用户放进上下文；
// bearer 通道在这里完成校验、刷 last_used_at、按 key 限流并写带 api_key_id 的审计。
func (a *Authenticator) Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// MCP 只接受 API key；先拒绝 cookie 通道，避免普通 REST 的 CSRF 规则覆盖 401（DESIGN.md §7.4、§11）。
		if c.Request.URL.Path == "/mcp" {
			if _, ok := bearerToken(c.Request); !ok {
				abortError(c, http.StatusUnauthorized, CodeUnauthorized, "")
				return
			}
		}
		if token, ok := bearerToken(c.Request); ok {
			a.authenticateKey(c, token)
			return
		}
		if u, ok := auth.CurrentUser(c); ok && u.Status == store.StatusActive {
			if isMutating(c.Request.Method) {
				sess, hasSession := auth.CurrentSession(c)
				token := c.GetHeader(auth.CSRFHeaderName)
				if !hasSession || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
					abortError(c, http.StatusForbidden, CodeCSRFFailed, "")
					return
				}
			}
			c.Set(string(ctxUserKey), u)
			c.Next()
			return
		}
		abortError(c, http.StatusUnauthorized, CodeUnauthorized, "")
	}
}

// AuthMiddleware 暴露与 /api/v1 完全相同的鉴权中间件，供内置 MCP 的 /mcp 端点复用
// （DESIGN.md §7.4：MCP 复用用户级 API Key，不另写一套鉴权）。
func (a *API) AuthMiddleware() gin.HandlerFunc { return a.authn.Auth() }

// authenticateKey 校验 bearer key 并把用户与 key 写进上下文。
func (a *Authenticator) authenticateKey(c *gin.Context, plaintext string) {
	ctx := c.Request.Context()
	key, err := a.keys.Authenticate(ctx, plaintext, a.now())
	if err != nil {
		// 撤销 / 过期 / 不存在都按 401 处理；错误信息区分原因，但绝不回显 key 内容。
		abortError(c, http.StatusUnauthorized, CodeInvalidAPIKey, apiKeyErrorMessage(err))
		return
	}
	u, err := a.users.ByID(ctx, key.UserID)
	if err != nil || u.Status != store.StatusActive {
		// 用户被禁用或删除后，其 key 立即失效（权限边界与网页登录一致）。
		abortError(c, http.StatusUnauthorized, CodeUnauthorized, "")
		return
	}

	limiter := a.read
	if isMutating(c.Request.Method) {
		limiter = a.write
	}
	if !limiter.Allow(fmt.Sprintf("key:%d", key.ID)) {
		c.Header("Retry-After", "60")
		abortError(c, http.StatusTooManyRequests, CodeRateLimited, "")
		return
	}

	c.Set(string(ctxUserKey), u)
	c.Set(string(ctxKeyKey), key)
	// 每次 key 调用写审计并带 api_key_id（DESIGN.md §7.2）；写失败不阻断请求。
	recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: store.Ptr(key.ID),
		Action:   "api.request",
		Detail:   map[string]any{"method": c.Request.Method, "path": c.Request.URL.Path},
	})
	c.Next()
}

// RequireScope 在 Auth 之后强制 scope。
//
// bearer 通道按 key 的 scopes 判断，并对 admin 追加账号角色复查；会话通道与网页登录权限
// 一致（登录用户可读可写可复习，admin scope 另需管理员角色）。
func (a *Authenticator) RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key, ok := CurrentAPIKey(c); ok {
			if !key.HasScope(scope) {
				abortError(c, http.StatusForbidden, CodeScopeRequired, scope)
				return
			}
			// admin 只能发给管理员账号（DESIGN.md §7.2）：key 的 scopes 不脱离账号角色，
			// 账号被降级后，遗留的 admin-scope key 立即失去 admin 面。角色每请求现查，
			// 不缓存，因此降级即时生效。
			if scope == store.ScopeAdmin {
				if u, ok := CurrentUser(c); !ok || u.Role != store.RoleAdmin {
					abortError(c, http.StatusForbidden, CodeScopeRequired, "")
					return
				}
			}
			c.Next()
			return
		}
		u, ok := CurrentUser(c)
		if !ok {
			abortError(c, http.StatusUnauthorized, CodeUnauthorized, "")
			return
		}
		if scope == store.ScopeAdmin && u.Role != store.RoleAdmin {
			abortError(c, http.StatusForbidden, CodeScopeRequired, "")
			return
		}
		c.Next()
	}
}

// CurrentUser 取当前请求的用户；bearer 与会话两条通道都由 Auth 统一写入上下文。
func CurrentUser(c *gin.Context) (*store.User, bool) {
	v, ok := c.Get(string(ctxUserKey))
	if !ok {
		return nil, false
	}
	u, ok := v.(*store.User)
	return u, ok
}

// CurrentAPIKey 取当前请求使用的 API key；会话通道返回 false。
func CurrentAPIKey(c *gin.Context) (*store.APIKey, bool) {
	v, ok := c.Get(string(ctxKeyKey))
	if !ok {
		return nil, false
	}
	k, ok := v.(*store.APIKey)
	return k, ok
}

// CurrentAPIKeyID 返回当前 key 的 id，供审计条目填写 api_key_id；会话通道为 nil。
func CurrentAPIKeyID(c *gin.Context) *uint64 {
	if k, ok := CurrentAPIKey(c); ok && k != nil {
		return store.Ptr(k.ID)
	}
	return nil
}

// bearerToken 从 Authorization 头解析 Bearer token；缺失或形态不对返回 false。
func bearerToken(r *http.Request) (string, bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(h[7:])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// isMutating 判断请求是否写操作；写操作使用更严的限流配额（DESIGN.md §7.2）。
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// apiKeyErrorMessage 把存储层的校验错误翻译成不含敏感信息的英文提示。
func apiKeyErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrAPIKeyRevoked):
		return "api key has been revoked"
	case errors.Is(err, store.ErrAPIKeyExpired):
		return "api key has expired"
	default:
		return "unknown or malformed api key"
	}
}
