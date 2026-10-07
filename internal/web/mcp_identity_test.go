package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mcp"
	"git.nite07.com/nite/engram/internal/store"
)

// newMcpWebServer 装配一个带 /api/v1 与 /mcp 的完整 web 服务（含会话中间件），用于验证
// /mcp 只接受 API key。返回服务与账号服务，供用例落用户并登录。
func newMcpWebServer(t *testing.T) (*Server, *auth.AccountService) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "mcpweb.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	keys := store.NewAPIKeyStore(db)
	accounts, err := auth.NewAccountService(users, sessions, keys, auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	apiSrv, err := api.New(api.Deps{
		DB: db, Logger: discardLogger(), Keys: keys, Users: users,
		Decks: store.NewDeckStore(db), Notes: store.NewNoteStore(db),
		Presets: store.NewPresetStore(db), Cards: store.NewCardStore(db), Auditor: auditor,
		ReadLimit: 100000, WriteLimit: 100000,
	})
	if err != nil {
		t.Fatalf("api.New() error = %v", err)
	}
	mcpSrv, err := mcp.New(mcp.Deps{API: apiSrv, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("mcp.New() error = %v", err)
	}
	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		UserLocale:    SessionUserLocale,
		Decks:         store.NewDeckStore(db),
		Notes:         store.NewNoteStore(db),
		Cards:         store.NewCardStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
		API: apiSrv,
		MCP: mcpSrv,
	})
	if err != nil {
		t.Fatalf("web.New() error = %v", err)
	}
	return srv, accounts
}

// TestMcpEndpointRejectsSessionCookie 覆盖 F10 规则 1：带有效会话 cookie、不带 API key
// 访问 /mcp 必须 401，且不得建立会话。
func TestMcpEndpointRejectsSessionCookie(t *testing.T) {
	srv, accounts := newMcpWebServer(t)
	if _, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "cookie-user", Email: "cookie-user@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	}); err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	// 登录走 SPA 的同源 JSON 端点，取回会话 cookie（这里不关心 CSRF）。
	cookie, headers := preSessionPair(t, srv, "/login")
	login := postJSON(srv, "/api/v1/auth/login", map[string]string{
		"username": "cookie-user", "password": "Sup3rSecret!",
	}, []*http.Cookie{cookie}, headers)
	if login.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/login = %d, want 200 (body %s)", login.Code, snippet(login.Body.String()))
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"0.0.1"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only POST /mcp status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if sid := rec.Header().Get("Mcp-Session-Id"); sid != "" {
		t.Fatalf("cookie-only POST /mcp established a session (%q), want none", sid)
	}
}
