package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// Session defaults: the cookie name skips the __Host- prefix because development can run over http (Secure=false).
const (
	DefaultSessionCookieName = "engram_session"
	DefaultSessionTTL        = 30 * 24 * time.Hour
)

// SessionConfig is the explicit configuration of the session middleware; construction fails when the secret is missing, and a predictable default secret is never used.
type SessionConfig struct {
	Secret     []byte
	CookieName string
	// Secure must be true in production; it stays true when tests use TLS httptest.
	Secure bool
	TTL    time.Duration
	// Now is an injectable clock, handy for testing expiry.
	Now func() time.Time
}

// ctxKey uses an unexported type as the context key so it cannot collide with other packages.
type ctxKey string

const (
	ctxUserKey    ctxKey = "auth.user"
	ctxSessionKey ctxKey = "auth.session"
)

// Manager handles issuing/validating the session cookie, revoking sessions, and the session and CSRF middleware pair.
type Manager struct {
	users      *store.UserStore
	sessions   *store.SessionStore
	secret     []byte
	cookieName string
	secure     bool
	ttl        time.Duration
	now        func() time.Time
}

// NewSessionManager constructs the session manager; an empty cfg.Secret or a missing dependency returns an English error.
func NewSessionManager(users *store.UserStore, sessions *store.SessionStore, cfg SessionConfig) (*Manager, error) {
	if users == nil || sessions == nil {
		return nil, errors.New("auth: user store and session store are required")
	}
	if len(cfg.Secret) == 0 {
		return nil, errors.New("auth: session secret must not be empty")
	}
	m := &Manager{
		users:      users,
		sessions:   sessions,
		secret:     cfg.Secret,
		cookieName: cfg.CookieName,
		secure:     cfg.Secure,
		ttl:        cfg.TTL,
		now:        cfg.Now,
	}
	if m.cookieName == "" {
		m.cookieName = DefaultSessionCookieName
	}
	if m.ttl <= 0 {
		m.ttl = DefaultSessionTTL
	}
	if m.now == nil {
		m.now = func() time.Time { return time.Now().UTC() }
	}
	return m, nil
}

// randomToken generates n bytes of high-entropy randomness and URL-safe encodes it.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SignValue returns "payload.HMAC-SHA256(payload)". The session cookie and the TOTP second-step credential share this one signing spot,
// so forging either is equally hard and there is no second key. It works when the payload contains no '.', but the separator is located with LastIndex,
// so a payload may contain '.' itself (e.g. "userID.expUnix").
func (m *Manager) SignValue(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyValue validates the signature and recovers the payload; any parse or signature problem returns false.
func (m *Manager) VerifyValue(value string) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i <= 0 || i == len(value)-1 {
		return "", false
	}
	payload, rawSig := value[:i], value[i+1:]
	sig, err := base64.RawURLEncoding.DecodeString(rawSig)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", false
	}
	return payload, true
}

// sign returns "sessionID.signature"; the session ID itself contains no '.', so the semantics match SignValue.
func (m *Manager) sign(id string) string { return m.SignValue(id) }

// verifyCookie validates the signature and recovers the session ID; any parse or signature problem counts as anonymous.
func (m *Manager) verifyCookie(value string) (string, bool) { return m.VerifyValue(value) }

// StartSession creates a new server-side session, hands out a signed cookie, and generates a CSRF token for that session.
func (m *Manager) StartSession(ctx context.Context, c *gin.Context, userID uint64) (*store.Session, error) {
	id, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := m.now()
	sess := &store.Session{
		ID:        id,
		UserID:    userID,
		CSRFToken: csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(m.ttl),
	}
	if err := m.sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	m.setCookie(c, id)
	return sess, nil
}

// Logout revokes the current session's server-side record and clears the cookie; with no session it still clears the cookie (idempotent).
func (m *Manager) Logout(ctx context.Context, c *gin.Context) error {
	if sess, ok := CurrentSession(c); ok {
		if err := m.sessions.Revoke(ctx, sess.ID, m.now()); err != nil {
			return err
		}
	}
	m.clearCookie(c)
	return nil
}

// RevokeAllForUser revokes all sessions of a user: password change, disable and forced logout go through here.
func (m *Manager) RevokeAllForUser(ctx context.Context, userID uint64) error {
	return m.sessions.RevokeAllForUser(ctx, userID, m.now())
}

// setCookie hands out the session cookie: HttpOnly + SameSite=Lax + Secure (in production), Path=/.
func (m *Manager) setCookie(c *gin.Context, id string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     m.cookieName,
		Value:    m.sign(id),
		Path:     "/",
		MaxAge:   int(m.ttl.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearCookie overwrites the client-side session with a same-named cookie that expires immediately.
func (m *Manager) clearCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     m.cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// CookieName returns the session cookie name, for tests and the login page to come.
func (m *Manager) CookieName() string { return m.cookieName }

// Middleware parses the session cookie and puts the user/session into the request context.
// Three server-side revocation paths converge here: the row was revoked, the cookie expired, the user was disabled/deleted.
func (m *Manager) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Request.Cookie(m.cookieName)
		if err != nil {
			c.Next()
			return
		}
		id, ok := m.verifyCookie(cookie.Value)
		if !ok {
			m.clearCookie(c)
			c.Next()
			return
		}
		ctx := c.Request.Context()
		sess, err := m.sessions.ByID(ctx, id)
		if err != nil || sess.RevokedAt != nil || !sess.ExpiresAt.After(m.now()) {
			m.clearCookie(c)
			c.Next()
			return
		}
		u, err := m.users.ByID(ctx, sess.UserID)
		if err != nil || u.Status != store.StatusActive {
			// The user is disabled or deleted: revoke the server-side session immediately even if the cookie is still valid.
			_ = m.sessions.Revoke(ctx, sess.ID, m.now())
			m.clearCookie(c)
			c.Next()
			return
		}
		c.Set(string(ctxUserKey), u)
		c.Set(string(ctxSessionKey), sess)
		// Last-seen time is display only; a failure does not interrupt the request.
		_ = m.sessions.Touch(ctx, sess.ID, m.now())
		c.Next()
	}
}

// RequireUser rejects unauthenticated requests (401); place it after Middleware and before protected routes.
func (m *Manager) RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := CurrentUser(c); !ok {
			// English internal error; user-facing text awaits the language pack (onboarding).
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "unauthorized", "message": "authentication required"},
			})
			return
		}
		c.Next()
	}
}

// CurrentUser takes the current user from the context.
func CurrentUser(c *gin.Context) (*store.User, bool) {
	v, ok := c.Get(string(ctxUserKey))
	if !ok {
		return nil, false
	}
	u, ok := v.(*store.User)
	return u, ok
}

// CurrentSession takes the current session from the context.
func CurrentSession(c *gin.Context) (*store.Session, bool) {
	v, ok := c.Get(string(ctxSessionKey))
	if !ok {
		return nil, false
	}
	s, ok := v.(*store.Session)
	return s, ok
}
