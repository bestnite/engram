package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestCardTypesEndpointReturnsRegistryDescriptions 覆盖 /api/v1/card-types：
// 返回 200、kinds 数量等于注册表题型数、每个元素都带 kind/label_key/fields。
func TestCardTypesEndpointReturnsRegistryDescriptions(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "card_types_owner", store.RoleUser)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/card-types", key.Plaintext, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/card-types = %d, want 200 (body %s)", code, raw)
	}
	var body struct {
		Kinds []struct {
			Kind     string            `json:"kind"`
			LabelKey string            `json:"label_key"`
			Fields   []json.RawMessage `json:"fields"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("card-types response is not JSON: %v (%s)", err, raw)
	}
	if want := len(cardtype.Kinds()); len(body.Kinds) != want {
		t.Fatalf("kinds has %d entries, want %d", len(body.Kinds), want)
	}
	for _, k := range body.Kinds {
		if k.Kind == "" || k.LabelKey == "" || len(k.Fields) == 0 {
			t.Errorf("entry is missing kind/label_key/fields: %+v", k)
		}
	}
}

// TestCardTypesEndpointRequiresAuth 是负例：未认证（无会话、无 API key）返回 401。
func TestCardTypesEndpointRequiresAuth(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()

	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/card-types", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/card-types without auth = %d, want 401 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeUnauthorized)
}
