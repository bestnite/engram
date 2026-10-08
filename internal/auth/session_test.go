package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// testEnv 把真实 SQLite、真实 store 与真实中间件串起来；不 mock 数据库（AGENTS.md §2.5）。
type testEnv struct {
	router   *gin.Engine
	manager  *Manager
	accounts *AccountService
	users    *store.UserStore
	sessions *store.SessionStore
	db       *gorm.DB
}

// newTestEnv 默认只装会话中间件；CSRF 由需要它的测试用 buildRouter 追加。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := NewAccountService(users, sessions, store.NewAPIKeyStore(db), NewPasswordHasher(fastParams()))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	manager, err := NewSessionManager(users, sessions, SessionConfig{
		Secret: []byte("test-only-session-secret-not-for-production"),
		Secure: true,
	})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	gin.SetMode(gin.TestMode)
	e := &testEnv{manager: manager, accounts: accounts, users: users, sessions: sessions, db: db}
	e.router = e.buildRouter(manager.Middleware())
	return e
}

// buildRouter 用给定中间件链装配同一组测试路由；调用方可按需插入 CSRF。
func (e *testEnv) buildRouter(middleware ...gin.HandlerFunc) *gin.Engine {
	router := gin.New()
	router.Use(middleware...)
	router.GET("/me", e.manager.RequireUser(), func(c *gin.Context) {
		u, _ := CurrentUser(c)
		c.JSON(http.StatusOK, gin.H{"id": u.ID, "username": u.Username})
	})
	router.POST("/echo", e.manager.RequireUser(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	router.POST("/logout", e.manager.RequireUser(), func(c *gin.Context) {
		if err := e.manager.Logout(c.Request.Context(), c); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return router
}

// createUser 建一个可用于登录的活跃用户。
func (e *testEnv) createUser(t *testing.T, username string) *store.User {
	t.Helper()
	u, err := e.accounts.CreateLocalUser(context.Background(), CreateUserInput{
		Username: username,
		Email:    username + "@example.com",
		Password: "Tr0ub4dor&3",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%s) error = %v", username, err)
	}
	return u
}

// startSession 通过真实的 StartSession 拿会话并捕获 Set-Cookie。
func (e *testEnv) startSession(t *testing.T, userID uint64) (string, *store.Session) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := e.manager.StartSession(context.Background(), c, userID)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == e.manager.CookieName() {
			return cookie.Value, sess
		}
	}
	t.Fatal("StartSession did not set a session cookie")
	return "", nil
}

// do 用给定 cookie 值向默认路由（无 CSRF）发一个请求。
func (e *testEnv) do(t *testing.T, method, path, cookieValue string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return doOn(e.router, e.manager.CookieName(), method, path, cookieValue, headers)
}

// doOn 向指定路由发请求；抽出共享逻辑供带 CSRF 的路由复用。
func doOn(router *gin.Engine, cookieName, method, path, cookieValue string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookieValue})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestSessionCookieFlags(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "alice")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := e.manager.StartSession(context.Background(), c, u.ID); err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	got := cookies[0]
	if !got.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if !got.Secure {
		t.Error("session cookie is not Secure")
	}
	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", got.SameSite)
	}
	if got.Path != "/" {
		t.Errorf("session cookie Path = %q, want /", got.Path)
	}
}

func TestSessionRejectsTamperedCookie(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "bob")
	value, _ := e.startSession(t, u.ID)
	// 篡改会话 ID 的首字符后必须被当成匿名。
	//
	// 不能用"改最后一个字符"来篡改：签名是 32 字节 HMAC，其 base64url 编码的最后
	// 一位只承载 4 个有效位，剩下 2 位是填充；把末位换成"高 4 位相同"的字符（例如
	// 原本是 'U' 时换成 'X'）解码出的签名字节完全一致，篡改等于没改，请求依旧 200。
	// 该末位取值为 16 个合法字符之一，因此旧写法约有 1/16 的概率随机失败——这是
	// 与数据库无关的用例缺陷。改 ID 一定让 HMAC 失配，结果确定。
	tampered := tamperSessionCookie(value)
	if rec := e.do(t, http.MethodGet, "/me", tampered, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered cookie status = %d, want 401", rec.Code)
	}
}

// tamperSessionCookie 把 cookie 值的首字符换成另一个 base64url 字符：
// 首字符属于会话 ID，改动后 HMAC 必然对不上原签名，等价于一次确定的篡改。
func tamperSessionCookie(value string) string {
	if value == "" {
		return "A"
	}
	if value[0] == 'A' {
		return "B" + value[1:]
	}
	return "A" + value[1:]
}

func TestSessionProtectedRoute(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "carol")
	if rec := e.do(t, http.MethodGet, "/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous /me status = %d, want 401", rec.Code)
	}
	value, _ := e.startSession(t, u.ID)
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusOK {
		t.Errorf("authenticated /me status = %d, want 200", rec.Code)
	}
}

// TestDisabledUserSessionRejected 是验收反面用例：禁用后下一次请求即被拒。
func TestDisabledUserSessionRejected(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "dave")
	value, _ := e.startSession(t, u.ID)
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusOK {
		t.Fatalf("pre-disable /me status = %d, want 200", rec.Code)
	}
	if err := e.accounts.DisableUser(context.Background(), u.ID); err != nil {
		t.Fatalf("DisableUser() error = %v", err)
	}
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("post-disable /me status = %d, want 401", rec.Code)
	}
}

// TestDisabledStatusAloneRejectsSession 证明即便会话行未被显式撤销，中间件也会因用户状态而拒绝。
func TestDisabledStatusAloneRejectsSession(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "erin")
	value, _ := e.startSession(t, u.ID)
	// 直接改状态，绕过 RevokeAllForUser，验证中间件的实时状态检查。
	if err := e.db.Model(&store.User{}).Where("id = ?", u.ID).
		Update("status", store.StatusDisabled).Error; err != nil {
		t.Fatalf("update status: %v", err)
	}
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled-by-status /me status = %d, want 401", rec.Code)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "frank")
	value, sess := e.startSession(t, u.ID)
	if rec := e.do(t, http.MethodPost, "/logout", value, nil); rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", rec.Code)
	}
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("post-logout /me status = %d, want 401", rec.Code)
	}
	if _, err := e.sessions.ByID(context.Background(), sess.ID); err != nil {
		t.Errorf("session row missing after logout: %v", err)
	}
}

func TestPasswordChangeInvalidatesAllSessions(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "grace")
	value, _ := e.startSession(t, u.ID)
	if err := e.accounts.ChangePassword(context.Background(), u.ID, "Tr0ub4dor&3", "N3w-Str0ng-Pass"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("post-password-change /me status = %d, want 401", rec.Code)
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "heidi")
	value, sess := e.startSession(t, u.ID)
	if err := e.db.Model(&store.Session{}).Where("id = ?", sess.ID).
		Update("expires_at", sess.CreatedAt.AddDate(0, 0, -1)).Error; err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if rec := e.do(t, http.MethodGet, "/me", value, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired /me status = %d, want 401", rec.Code)
	}
}

func TestAuthenticate(t *testing.T) {
	e := newTestEnv(t)
	e.createUser(t, "ivan")
	cases := []struct {
		name     string
		username string
		password string
		wantErr  error
	}{
		{name: "correct", username: "ivan", password: "Tr0ub4dor&3", wantErr: nil},
		{name: "wrong password", username: "ivan", password: "wrong-password", wantErr: ErrInvalidCredentials},
		{name: "unknown user", username: "nobody", password: "Tr0ub4dor&3", wantErr: ErrInvalidCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.accounts.Authenticate(context.Background(), tc.username, tc.password)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Authenticate() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
