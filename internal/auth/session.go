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

	"example.com/flashcard/internal/store"
)

// 会话默认值：cookie 名不用 __Host- 前缀，因为开发态可跑在 http（Secure=false）下。
const (
	DefaultSessionCookieName = "fc_session"
	DefaultSessionTTL        = 30 * 24 * time.Hour
)

// SessionConfig 是会话中间件的显式配置；缺少密钥时构造失败，绝不使用可预测的默认密钥。
type SessionConfig struct {
	Secret     []byte
	CookieName string
	// Secure 在生产必须为 true（DESIGN.md §4.3、§11）；测试用 TLS httptest 时保持 true。
	Secure bool
	TTL    time.Duration
	// Now 可注入时钟，便于测试过期。
	Now func() time.Time
}

// ctxKey 用未导出类型做上下文键，避免与其他包冲突。
type ctxKey string

const (
	ctxUserKey    ctxKey = "auth.user"
	ctxSessionKey ctxKey = "auth.session"
)

// Manager 负责会话 cookie 的签发/校验、会话作废，以及会话与 CSRF 两个中间件。
type Manager struct {
	users      *store.UserStore
	sessions   *store.SessionStore
	secret     []byte
	cookieName string
	secure     bool
	ttl        time.Duration
	now        func() time.Time
}

// NewSessionManager 构造会话管理器；cfg.Secret 为空或无依赖时返回英文错误。
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

// randomToken 生成 n 字节的高熵随机值并做 URL 安全编码。
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sign 返回 "会话ID.签名"，签名是对 ID 的 HMAC-SHA256；cookie 因此不可伪造（"签名 cookie"）。
func (m *Manager) sign(id string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(id))
	return id + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyCookie 校验签名并取回会话 ID；任何解析/签名问题都视作匿名。
func (m *Manager) verifyCookie(value string) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i <= 0 || i == len(value)-1 {
		return "", false
	}
	id, rawSig := value[:i], value[i+1:]
	sig, err := base64.RawURLEncoding.DecodeString(rawSig)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(id))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", false
	}
	return id, true
}

// StartSession 新建服务端会话、下发签名 cookie，并为该会话生成 CSRF token。
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

// Logout 作废当前会话的服务端记录并清除 cookie；没有会话时也清除 cookie（幂等）。
func (m *Manager) Logout(ctx context.Context, c *gin.Context) error {
	if sess, ok := CurrentSession(c); ok {
		if err := m.sessions.Revoke(ctx, sess.ID, m.now()); err != nil {
			return err
		}
	}
	m.clearCookie(c)
	return nil
}

// RevokeAllForUser 作废某用户的全部会话：改密码、禁用、强制下线走这里（DESIGN.md §11）。
func (m *Manager) RevokeAllForUser(ctx context.Context, userID uint64) error {
	return m.sessions.RevokeAllForUser(ctx, userID, m.now())
}

// setCookie 下发会话 cookie：HttpOnly + SameSite=Lax +（生产）Secure，Path=/。
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

// clearCookie 用一个立即过期的同名 cookie 覆盖客户端会话。
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

// CookieName 返回会话 cookie 名，供测试与后续登录页使用。
func (m *Manager) CookieName() string { return m.cookieName }

// Middleware 解析会话 cookie 并把用户/会话放进请求上下文。
// 三种服务端作废路径在这里统一收敛：行被撤销、cookie 过期、用户被禁用/删除。
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
			// 用户被禁用或已删除：即便 cookie 仍有效也立即作废服务端会话（DESIGN.md §11）。
			_ = m.sessions.Revoke(ctx, sess.ID, m.now())
			m.clearCookie(c)
			c.Next()
			return
		}
		c.Set(string(ctxUserKey), u)
		c.Set(string(ctxSessionKey), sess)
		// 最后活跃时间只用于展示，失败不打断请求。
		_ = m.sessions.Touch(ctx, sess.ID, m.now())
		c.Next()
	}
}

// RequireUser 拒绝未登录请求（401）；放在 Middleware 之后、受保护路由之前。
func (m *Manager) RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := CurrentUser(c); !ok {
			// 英文内部错误；面向用户文案待语言包（M0-8/M1-4 接入）。
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "unauthorized", "message": "authentication required"},
			})
			return
		}
		c.Next()
	}
}

// CurrentUser 从上下文取当前用户。
func CurrentUser(c *gin.Context) (*store.User, bool) {
	v, ok := c.Get(string(ctxUserKey))
	if !ok {
		return nil, false
	}
	u, ok := v.(*store.User)
	return u, ok
}

// CurrentSession 从上下文取当前会话。
func CurrentSession(c *gin.Context) (*store.Session, bool) {
	v, ok := c.Get(string(ctxSessionKey))
	if !ok {
		return nil, false
	}
	s, ok := v.(*store.Session)
	return s, ok
}
