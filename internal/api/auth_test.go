package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAuthenticatorNegativeCases 覆盖四种反面用例：
// 缺 scope、过期 key、已撤销 key、限流耗尽，外加无凭据。
func TestAuthenticatorNegativeCases(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		setup      func(t *testing.T, env *testEnv, user *store.User) string
		repeat     int
		wantStatus int
		wantCode   string
	}{
		{
			name:   "no credentials",
			method: http.MethodGet, path: "/api/v1/decks",
			setup:      func(t *testing.T, env *testEnv, user *store.User) string { return "" },
			repeat:     1,
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeUnauthorized,
		},
		{
			name:   "missing scope",
			method: http.MethodPost, path: "/api/v1/decks",
			setup: func(t *testing.T, env *testEnv, user *store.User) string {
				k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
				return "Bearer " + k.Plaintext
			},
			repeat:     1,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeScopeRequired,
		},
		{
			name:   "expired key",
			method: http.MethodGet, path: "/api/v1/decks",
			setup: func(t *testing.T, env *testEnv, user *store.User) string {
				past := env.now.Add(-time.Hour)
				k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, &past)
				return "Bearer " + k.Plaintext
			},
			repeat:     1,
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeInvalidAPIKey,
		},
		{
			name:   "revoked key",
			method: http.MethodGet, path: "/api/v1/decks",
			setup: func(t *testing.T, env *testEnv, user *store.User) string {
				k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
				if err := env.keys.Revoke(context.Background(), user.ID, k.Key.ID, env.now); err != nil {
					t.Fatalf("revoke: %v", err)
				}
				return "Bearer " + k.Plaintext
			},
			repeat:     1,
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeInvalidAPIKey,
		},
		{
			name:   "rate limit exhausted",
			method: http.MethodGet, path: "/api/v1/decks",
			setup: func(t *testing.T, env *testEnv, user *store.User) string {
				k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
				return "Bearer " + k.Plaintext
			},
			// read limit 配成 2：第 3 次请求必须被拒。
			repeat:     3,
			wantStatus: http.StatusTooManyRequests,
			wantCode:   CodeRateLimited,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 2, 2)
			user := seedUser(t, env.db, "user_"+safeName(tc.name), store.RoleUser)
			header := tc.setup(t, env, user)
			router := env.router()

			var rec *httptest.ResponseRecorder
			for i := 0; i < tc.repeat; i++ {
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
				req.Header.Set("Content-Type", "application/json")
				if header != "" {
					req.Header.Set("Authorization", header)
				}
				rec = httptest.NewRecorder()
				router.ServeHTTP(rec, req)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("last status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q (body %s)", body.Error.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// TestAuthenticatorWritesAuditWithKeyID 断言 bearer 调用写下带 api_key_id 的审计行。
func TestAuthenticatorWritesAuditWithKeyID(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "audit_user", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	router := env.router()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer "+k.Plaintext)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var row store.AuditLog
	if err := env.db.Where("api_key_id = ?", k.Key.ID).First(&row).Error; err != nil {
		t.Fatalf("audit row with api_key_id not found: %v", err)
	}
	if row.APIKeyID == nil || *row.APIKeyID != k.Key.ID {
		t.Errorf("audit api_key_id = %v, want %d", row.APIKeyID, k.Key.ID)
	}
	if row.UserID == nil || *row.UserID != user.ID {
		t.Errorf("audit user_id = %v, want %d", row.UserID, user.ID)
	}
	if row.Action != "api.request" {
		t.Errorf("audit action = %q, want %q", row.Action, "api.request")
	}
}
