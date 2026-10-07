package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// keyCountFor 返回某用户当前的 key 数，用于断言被拒的创建没有落库。
func keyCountFor(t *testing.T, keys *store.APIKeyStore, userID uint64) int {
	t.Helper()
	list, err := keys.ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	return len(list)
}

// TestKeysEndpointRequiresKeysScope 覆盖这条规则：/api/v1/keys 的门槛是第五档 keys。
// 普通用户凭 keys scope 可以管理自己的 key（且只看得到自己的），read scope 仍被拒。
func TestKeysEndpointRequiresKeysScope(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "keys_scope_owner", store.RoleUser)
	other := seedUser(t, env.db, "keys_scope_other", store.RoleUser)
	selfKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeKeys}, nil)
	seedKey(t, env.keys, other.ID, []string{store.ScopeRead}, nil)

	t.Run("keys scope lists only own keys", func(t *testing.T) {
		code, raw := doJSON(t, router, http.MethodGet, "/api/v1/keys", selfKey.Plaintext, "")
		if code != http.StatusOK {
			t.Fatalf("GET /api/v1/keys with keys scope = %d, want 200 (body %s)", code, raw)
		}
		var body struct {
			Keys []store.APIKey `json:"keys"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("keys response is not JSON: %v (%s)", err, raw)
		}
		if len(body.Keys) != 1 || body.Keys[0].ID != selfKey.Key.ID {
			t.Fatalf("keys = %+v, want exactly the caller's own key %d", body.Keys, selfKey.Key.ID)
		}
		for _, k := range body.Keys {
			if k.UserID != owner.ID {
				t.Errorf("list leaked a key owned by user %d", k.UserID)
			}
		}
	})

	t.Run("read scope denied", func(t *testing.T) {
		readKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
		code, raw := doJSON(t, router, http.MethodGet, "/api/v1/keys", readKey.Plaintext, "")
		if code != http.StatusForbidden {
			t.Fatalf("GET /api/v1/keys with read scope = %d, want 403 (body %s)", code, raw)
		}
		assertErrorCode(t, raw, CodeScopeRequired)
	})
}

// TestNonAdminCannotGrantAdminScopeOverREST 覆盖这条规则：非管理员在任何路径都不能创建
// admin scope 的 key——即便是持有 keys scope（或修复前遗留的 admin scope key）的账号，
// REST 也必须拒绝且不落库。只持 read 的 key 连 keys 门槛都过不了。
func TestNonAdminCannotGrantAdminScopeOverREST(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()

	cases := []struct {
		name     string
		scopes   []string
		wantCode string
	}{
		{"keys scope key", []string{store.ScopeKeys}, CodeScopeNotGrantable},
		{"legacy admin scope key", []string{store.ScopeAdmin}, CodeScopeNotGrantable},
		{"read scope key", []string{store.ScopeRead}, CodeScopeRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := seedUser(t, env.db, "nogrante_"+safeName(tc.name), store.RoleUser)
			key := seedKey(t, env.keys, user.ID, tc.scopes, nil)
			before := keyCountFor(t, env.keys, user.ID)
			code, raw := doJSON(t, router, http.MethodPost, "/api/v1/keys", key.Plaintext,
				`{"name":"evil","scopes":["admin"]}`)
			if code != http.StatusForbidden {
				t.Fatalf("POST admin scope as non-admin = %d, want 403 (body %s)", code, raw)
			}
			assertErrorCode(t, raw, tc.wantCode)
			if after := keyCountFor(t, env.keys, user.ID); after != before {
				t.Errorf("rejected create wrote a key: %d -> %d", before, after)
			}
		})
	}
}

// TestAdminCanGrantAdminScopeOverREST 覆盖这条规则：管理员可创建 admin scope 的 key，
// 且 admin scope 蕴含 keys，能通过 /keys 的 keys 门槛。
func TestAdminCanGrantAdminScopeOverREST(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	admin := seedUser(t, env.db, "rest_grant_admin", store.RoleAdmin)
	key := seedKey(t, env.keys, admin.ID, []string{store.ScopeAdmin}, nil)

	// admin 蕴含 keys：admin-scope key 能列出自己的 key。
	if code, raw := doJSON(t, router, http.MethodGet, "/api/v1/keys", key.Plaintext, ""); code != http.StatusOK {
		t.Fatalf("GET /api/v1/keys with admin scope = %d, want 200 (body %s)", code, raw)
	}
	// 管理员可以再建一把 admin scope 的 key。
	code, raw := doJSON(t, router, http.MethodPost, "/api/v1/keys", key.Plaintext, `{"name":"ops","scopes":["admin"]}`)
	if code != http.StatusCreated {
		t.Fatalf("POST admin scope as admin = %d, want 201 (body %s)", code, raw)
	}
	var body struct {
		Key struct {
			Scopes string `json:"scopes"`
		} `json:"key"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("create response is not JSON: %v (%s)", err, raw)
	}
	if body.Key.Scopes != store.ScopeAdmin {
		t.Errorf("created scopes = %q, want %q", body.Key.Scopes, store.ScopeAdmin)
	}
}

// TestAdminScopeKeyLosesAdminSurfaceAfterDemotion 覆盖这条规则的第三点：bearer 通道的
// admin scope 不是账号角色的替代品——账号被降级后，同一把 admin-scope key 立即失去
// admin 面（每请求查库拿 role 的既有路径）。探针路由只要求 admin scope，代表未来的
// 真正 admin 面。
func TestAdminScopeKeyLosesAdminSurfaceAfterDemotion(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	u := seedUser(t, env.db, "demoted_admin", store.RoleAdmin)
	key := seedKey(t, env.keys, u.ID, []string{store.ScopeAdmin}, nil)

	probe := gin.New()
	probe.GET("/admin-only", env.api.authn.Auth(), env.api.authn.RequireScope(store.ScopeAdmin),
		func(c *gin.Context) { c.Status(http.StatusOK) })

	if code, raw := doJSON(t, probe, http.MethodGet, "/admin-only", key.Plaintext, ""); code != http.StatusOK {
		t.Fatalf("admin-scope key before demotion = %d, want 200 (body %s)", code, raw)
	}
	if err := env.db.Model(&store.User{}).Where("id = ?", u.ID).Update("role", store.RoleUser).Error; err != nil {
		t.Fatalf("demote user: %v", err)
	}
	code, raw := doJSON(t, probe, http.MethodGet, "/admin-only", key.Plaintext, "")
	if code != http.StatusForbidden {
		t.Fatalf("admin-scope key after demotion = %d, want 403 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeScopeRequired)
}
