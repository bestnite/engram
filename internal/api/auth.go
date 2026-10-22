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

// Context keys use an unexported type so they cannot collide with other packages.
type ctxKey string

const (
	ctxUserKey ctxKey = "api.user"
	ctxKeyKey  ctxKey = "api.api_key"
)

// AuthConfig is the explicit configuration of the auth middleware; construction fails when a dependency is missing.
type AuthConfig struct {
	Keys    *store.APIKeyStore
	Users   *store.UserStore
	Auditor *auth.Auditor
	Logger  *slog.Logger
	// Now is an injectable clock, making expiry and rate-limit windows testable.
	Now func() time.Time
	// ReadLimit / WriteLimit / RateWindow: see RateLimiter.
	ReadLimit  int
	WriteLimit int
	RateWindow time.Duration
}

// Authenticator implementation: one request pipeline accepts both session cookies and bearer keys,
// both resolve to the same user, and RequireScope then enforces scope.
type Authenticator struct {
	keys    *store.APIKeyStore
	users   *store.UserStore
	auditor *auth.Auditor
	logger  *slog.Logger
	now     func() time.Time
	read    *RateLimiter
	write   *RateLimiter
}

// NewAuthenticator builds the auth middleware; Keys / Users are required.
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

// Auth is the entry middleware for /api/v1: it looks at the bearer key first, then falls back to the session cookie.
//
// The session channel relies on the global session middleware (wired up by web.New) having already put the user into the context;
// the bearer channel does its validation here: refreshing last_used_at, rate limiting per key, and writing an audit entry carrying api_key_id.
func (a *Authenticator) Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// MCP only accepts API keys; reject the cookie channel first so the CSRF rules of plain REST do not mask the 401.
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

// AuthMiddleware exposes exactly the same auth middleware as /api/v1, for the built-in MCP /mcp endpoint to reuse
// (MCP reuses user-level API keys instead of growing a second auth stack).
func (a *API) AuthMiddleware() gin.HandlerFunc { return a.authn.Auth() }

// authenticateKey validates the bearer key and puts the user and the key into the context.
func (a *Authenticator) authenticateKey(c *gin.Context, plaintext string) {
	ctx := c.Request.Context()
	key, err := a.keys.Authenticate(ctx, plaintext, a.now())
	if err != nil {
		// Revoked / expired / nonexistent are all treated as 401; the error message distinguishes the reason but never echoes the key.
		abortError(c, http.StatusUnauthorized, CodeInvalidAPIKey, apiKeyErrorMessage(err))
		return
	}
	u, err := a.users.ByID(ctx, key.UserID)
	if err != nil || u.Status != store.StatusActive {
		// Once a user is suspended or deleted their keys stop working immediately (the permission boundary matches web login).
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
	// Every key call writes an audit entry carrying api_key_id; a failed write does not block the request.
	recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
		UserID:   store.Ptr(u.ID),
		APIKeyID: store.Ptr(key.ID),
		Action:   "api.request",
		Detail:   map[string]any{"method": c.Request.Method, "path": c.Request.URL.Path},
	})
	c.Next()
}

// RequireScope enforces scope after Auth.
//
// The bearer channel decides from the key's scopes and additionally re-checks the account role for admin; the session channel matches
// web login permissions (a logged-in user may read, write and review, while admin scope also requires the admin role).
func (a *Authenticator) RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if key, ok := CurrentAPIKey(c); ok {
			if !key.HasScope(scope) {
				abortError(c, http.StatusForbidden, CodeScopeRequired, scope)
				return
			}
			// admin can only be issued to admin accounts: a key's scopes never float free of the account role,
			// so once an account is demoted a leftover admin-scope key immediately loses the admin surface. The role is looked up on every request,
			// never cached, so demotion takes effect immediately.
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

// CurrentUser returns the user of the current request; Auth writes it into the context uniformly for both the bearer and session channels.
func CurrentUser(c *gin.Context) (*store.User, bool) {
	v, ok := c.Get(string(ctxUserKey))
	if !ok {
		return nil, false
	}
	u, ok := v.(*store.User)
	return u, ok
}

// CurrentAPIKey returns the API key used by the current request; the session channel returns false.
func CurrentAPIKey(c *gin.Context) (*store.APIKey, bool) {
	v, ok := c.Get(string(ctxKeyKey))
	if !ok {
		return nil, false
	}
	k, ok := v.(*store.APIKey)
	return k, ok
}

// CurrentAPIKeyID returns the id of the current key, for audit entries to fill in api_key_id; it is nil on the session channel.
func CurrentAPIKeyID(c *gin.Context) *uint64 {
	if k, ok := CurrentAPIKey(c); ok && k != nil {
		return store.Ptr(k.ID)
	}
	return nil
}

// bearerToken parses the Bearer token from the Authorization header; missing or malformed returns false.
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

// isMutating reports whether the request is a write; writes use the stricter rate-limit quota.
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// apiKeyErrorMessage turns a store-level validation error into an English message free of sensitive information.
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
