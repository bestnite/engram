package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// newRenderServer builds a renderable test server; userLocale simulates M1's user language setting.
// The home page only renders once onboarding is complete now, so seed an active admin here.
// mut optionally rewrites the wiring parameters (for example setting Deps.Version).
func newRenderServer(t *testing.T, userLocale func(c *gin.Context) string, mut ...func(*Deps)) *Server {
	t.Helper()
	srv := newFreshServer(t, userLocale, mut...)
	seedActiveAdmin(t, srv)
	return srv
}

// newFreshServer builds a test server with no users at all, equivalent to "installed but not yet set up".
func newFreshServer(t *testing.T, userLocale func(c *gin.Context) string, mut ...func(*Deps)) *Server {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "render.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	deps := Deps{
		DB:            db,
		Users:         store.NewUserStore(db),
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		UserLocale:    userLocale,
	}
	for _, m := range mut {
		m(&deps)
	}
	srv, err := New("127.0.0.1:0", deps)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv
}

// seedActiveAdmin inserts an active admin so the instance is past the first-run window.
func seedActiveAdmin(t *testing.T, srv *Server) {
	t.Helper()
	admin := &store.User{
		Username: "admin", Email: "admin@example.com", DisplayName: "Admin",
		Role: store.RoleAdmin, Status: store.StatusActive, Locale: "zh-CN",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := srv.users.Create(context.Background(), admin); err != nil {
		t.Fatalf("seed active admin: %v", err)
	}
}

func get(t *testing.T, srv *Server, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestStaticRouteServesEmbeddedAsset asserts the /static/v/<hash>/... route is wired up and returns content.
func TestStaticRouteServesEmbeddedAsset(t *testing.T) {
	srv := newRenderServer(t, nil)
	url := srv.assets.URL("js/pwa.js")
	if url == "" {
		t.Fatal("pwa asset is not embedded")
	}
	rec := get(t, srv, url, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("served static asset is empty")
	}
}

// TestHomeFirstRunRedirect asserts that GET / 303s to /setup inside the first-run window, the window being bounded by "is there an active admin" (acceptance point).
// When an admin exists, GET / goes to the SPA app shell (the client router renders the home page).
func TestHomeFirstRunRedirect(t *testing.T) {
	tests := []struct {
		name     string
		seed     *store.User
		wantCode int
		wantLoc  string
	}{
		{name: "no user at all", wantCode: http.StatusSeeOther, wantLoc: "/setup"},
		{
			name: "admin already active",
			seed: &store.User{
				Username: "admin", Email: "admin@example.com", DisplayName: "Admin",
				Role: store.RoleAdmin, Status: store.StatusActive, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusOK,
		},
		{
			// A disabled admin does not count: the onboarding window should not close because of a disabled account (CountActiveAdmins' status filter).
			name: "admin disabled",
			seed: &store.User{
				Username: "old-admin", Email: "old-admin@example.com", DisplayName: "Old Admin",
				Role: store.RoleAdmin, Status: store.StatusDisabled, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusSeeOther, wantLoc: "/setup",
		},
		{
			// A plain user does not close the window either: what is missing is an admin, not just anyone.
			name: "ordinary user only",
			seed: &store.User{
				Username: "member", Email: "member@example.com", DisplayName: "Member",
				Role: store.RoleUser, Status: store.StatusActive, Locale: "zh-CN", Timezone: "UTC",
			},
			wantCode: http.StatusSeeOther, wantLoc: "/setup",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFreshServer(t, nil)
			if tc.seed != nil {
				tc.seed.CreatedAt = time.Now().UTC()
				if err := srv.users.Create(context.Background(), tc.seed); err != nil {
					t.Fatalf("create seed user: %v", err)
				}
			}
			rec := get(t, srv, "/", nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("GET / status = %d, want %d", rec.Code, tc.wantCode)
			}
			if loc := rec.Header().Get("Location"); loc != tc.wantLoc {
				t.Errorf("Location = %q, want %q", loc, tc.wantLoc)
			}
			if tc.wantLoc != "" {
				// Inside the first-run window the home page renders none of its own content, only a redirect (gin appends a bit of link text).
				if strings.Contains(rec.Body.String(), `<div id="app">`) {
					t.Error("first-run redirect returned the SPA shell; /setup must win")
				}
				return
			}
			if body := rec.Body.String(); !strings.Contains(body, `<div id="app">`) {
				t.Errorf("home after setup is not the SPA shell: %s", snippet(body))
			}
		})
	}
}

// TestHomeFirstRunRedirectDropsLang asserts the redirect carries no ?lang: the setup page resolves the language itself from Accept-Language and the cookie.
func TestHomeFirstRunRedirectDropsLang(t *testing.T) {
	srv := newFreshServer(t, nil)
	rec := get(t, srv, "/?lang=en", map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /?lang=en status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/setup" {
		t.Errorf("Location = %q, want /setup (the query must not be carried over)", loc)
	}
}

// snippet truncates a page for failure messages, so the whole page does not end up in the test log.
func snippet(body string) string {
	const limit = 400
	if len(body) <= limit {
		return body
	}
	return body[:limit] + "..."
}
