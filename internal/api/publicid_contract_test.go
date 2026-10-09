package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckListExposesOnlyPublicIDs 断言对外响应里的 id 是不透明的 public id，
// 自增数字主键不出现在任何字段里——这是「路由与 JSON 不再暴露自增 id」的核心不变量。
func TestDeckListExposesOnlyPublicIDs(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "pubid_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)

	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/decks", key.Plaintext, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/decks = %d, want 200 (body %s)", code, raw)
	}
	var body struct {
		Decks []struct {
			ID       string `json:"id"`
			PresetID string `json:"preset_id"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decks response is not JSON: %v (%s)", err, raw)
	}
	if len(body.Decks) != 1 {
		t.Fatalf("decks = %+v, want exactly one", body.Decks)
	}
	got := body.Decks[0]

	if got.ID != deck.PublicID {
		t.Errorf("deck id = %q, want the public id %q", got.ID, deck.PublicID)
	}
	if _, err := uuid.Parse(got.ID); err != nil {
		t.Errorf("deck id %q is not a UUID: %v", got.ID, err)
	}
	// 数字主键即使与 public id 恰好同形也不该出现；这里显式挡住自增主键本身。
	if numeric := strconv.FormatUint(deck.ID, 10); got.ID == numeric {
		t.Errorf("deck id is the numeric primary key %s, want an opaque public id", numeric)
	}
	if _, err := uuid.Parse(got.PresetID); err != nil {
		t.Errorf("preset_id %q is not a UUID: %v", got.PresetID, err)
	}
}
