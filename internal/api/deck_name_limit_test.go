package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// F27b: the REST create-deck entry shares the 200-character deck name bound with the store layer;
// over the bound returns the stable code deck_name_invalid, while names within it are created as usual.
func TestCreateDeckNameLimit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "deck_name_api", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()

	// 200 characters: created, returns 201.
	okBody, _ := json.Marshal(map[string]string{"name": strings.Repeat("a", 200)})
	status, raw := doJSON(t, router, http.MethodPost, "/api/v1/decks", k.Plaintext, string(okBody))
	if status != http.StatusCreated {
		t.Fatalf("POST /api/v1/decks (200-char name) status = %d, want 201 (body %s)", status, raw)
	}

	// 201 characters: rejected, code is deck_name_invalid.
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
