package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// keyCountFor returns a user's current key count, for asserting that a rejected creation was not persisted.
func keyCountFor(t *testing.T, keys *store.APIKeyStore, userID uint64) int {
	t.Helper()
	list, err := keys.ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser() error = %v", err)
	}
	return len(list)
}

// TestKeysEndpointRequiresKeysScope covers this rule: the threshold for /api/v1/keys is the fifth level, keys.
// An ordinary user with keys scope can manage their own keys (and only sees their own), while read scope is still rejected.
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
		// Only your own key is listed: exactly one, and it is the caller's. APIKey no longer exposes user_id
		// (it is redundant in your own key list), so "nothing of someone else's mixed in" is asserted by both the count and the id.
		if len(body.Keys) != 1 || body.Keys[0].PublicID != selfKey.Key.PublicID {
			t.Fatalf("keys = %+v, want exactly the caller's own key %s", body.Keys, selfKey.Key.PublicID)
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

// TestNonAdminCannotGrantAdminScopeOverREST covers this rule: a non-admin cannot create an
// admin-scope key on any path - an account holding keys scope (or a leftover admin-scope key from before the fix) still gets
// REST reject it and persists nothing. A key holding only read does not even clear the keys threshold.
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

// TestAdminCanGrantAdminScopeOverREST covers this rule: an admin can create admin-scope keys,
// and admin scope implies keys, so it clears the keys threshold on /keys.
func TestAdminCanGrantAdminScopeOverREST(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	admin := seedUser(t, env.db, "rest_grant_admin", store.RoleAdmin)
	key := seedKey(t, env.keys, admin.ID, []string{store.ScopeAdmin}, nil)

	// admin implies keys: an admin-scope key can list its own keys.
	if code, raw := doJSON(t, router, http.MethodGet, "/api/v1/keys", key.Plaintext, ""); code != http.StatusOK {
		t.Fatalf("GET /api/v1/keys with admin scope = %d, want 200 (body %s)", code, raw)
	}
	// The admin can create another admin-scope key.
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

// TestAdminScopeKeyLosesAdminSurfaceAfterDemotion covers the third point of this rule: on the bearer channel,
// admin scope is no substitute for the account role - once the account is demoted, the same admin-scope key immediately loses
// the admin surface (the existing per-request database lookup for the role). The probe route requires only admin scope, standing in for future
// real admin surfaces.
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
