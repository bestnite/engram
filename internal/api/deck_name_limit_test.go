package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// F27b：REST 建卡组的入口与 store 层共用 200 字符卡组名界限；
// 超限返回稳定 code deck_name_invalid，界限内的名字照常建成。
func TestCreateDeckNameLimit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "deck_name_api", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()

	// 200 字符：建成，返回 201。
	okBody, _ := json.Marshal(map[string]string{"name": strings.Repeat("a", 200)})
	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/decks", k.Plaintext, string(okBody))
	if status != http.StatusCreated {
		t.Fatalf("POST /api/v1/decks (200-char name) status = %d, want 201 (body %s)", status, raw)
	}

	// 201 字符：拒绝，code 为 deck_name_invalid。
	badBody, _ := json.Marshal(map[string]string{"name": strings.Repeat("a", 201)})
	status, raw = doJSON(t, router, http.MethodPost, "/api/v1/decks", k.Plaintext, string(badBody))
	if status != http.StatusBadRequest {
		t.Fatalf("POST /api/v1/decks (201-char name) status = %d, want 400 (body %s)", status, raw)
	}
	var out struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode error body: %v (body %s)", err, raw)
	}
	if out.Error.Code != CodeDeckNameInvalid {
		t.Fatalf("error code = %q, want %q (body %s)", out.Error.Code, CodeDeckNameInvalid, raw)
	}
	if out.Error.Message == "" {
		t.Fatalf("error message is empty: %s", raw)
	}
}
