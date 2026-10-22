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

// testEnv wires up real SQLite, real stores and the real middleware; the database is not mocked (AGENTS.md §2.5).
type testEnv struct {
	router   *gin.Engine
	manager  *Manager
	accounts *AccountService
	users    *store.UserStore
	sessions *store.SessionStore
	db       *gorm.DB
}

// newTestEnv installs only the session middleware by default; CSRF is appended by tests that need it via buildRouter.
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

// buildRouter assembles the same set of test routes with the given middleware chain; callers can insert CSRF as needed.
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

// createUser creates an active user that can log in.
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

// startSession obtains a session through the real StartSession and captures Set-Cookie.
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

// do sends one request to the default route (no CSRF) with the given cookie value.
func (e *testEnv) do(t *testing.T, method, path, cookieValue string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return doOn(e.router, e.manager.CookieName(), method, path, cookieValue, headers)
}

// doOn sends a request to the specified route; the shared logic is factored out for reuse by routes with CSRF.
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
	// Tampering with the first character of the session ID must be treated as anonymous.
	//
	// "Change the last character" cannot be used to tamper: the signature is a 32-byte HMAC whose base64url encoding has a final
	// character carrying only 4 significant bits, with the remaining 2 bits as padding; replacing the last character with one whose
	// "top 4 bits are identical" (e.g. 'X' in place of 'U') decodes to exactly the same signature bytes, so the tampering amounts to nothing and the request still gets 200.
	// That position takes one of 16 legal characters, so the old wording failed at random roughly 1 time in 16 — a
	// case defect unrelated to the database. Changing the ID always breaks the HMAC, so the result is deterministic.
	tampered := tamperSessionCookie(value)
	if rec := e.do(t, http.MethodGet, "/me", tampered, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered cookie status = %d, want 401", rec.Code)
	}
}

// tamperSessionCookie replaces the first character of the cookie value with another base64url character:
// the first character belongs to the session ID, so the HMAC necessarily no longer matches the original signature — a guaranteed tampering.
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

// TestDisabledUserSessionRejected is the negative acceptance case: after disabling, the next request is rejected.
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

// TestDisabledStatusAloneRejectsSession proves that the middleware rejects on user status even when the session row was not explicitly revoked.
func TestDisabledStatusAloneRejectsSession(t *testing.T) {
	e := newTestEnv(t)
	u := e.createUser(t, "erin")
	value, _ := e.startSession(t, u.ID)
	// Change the status directly, bypassing RevokeAllForUser, to verify the middleware's live status check.
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
