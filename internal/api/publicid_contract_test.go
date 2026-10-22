package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckListExposesOnlyPublicIDs asserts the ids in external responses are opaque public ids,
// with the auto-increment numeric primary key appearing in no field - the core invariant of "routes and JSON no longer expose auto-increment ids".
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
	// The numeric primary key must not appear even if it happens to look like the public id; here the auto-increment key itself is explicitly blocked.
	if numeric := strconv.FormatUint(deck.ID, 10); got.ID == numeric {
		t.Errorf("deck id is the numeric primary key %s, want an opaque public id", numeric)
	}
	if _, err := uuid.Parse(got.PresetID); err != nil {
		t.Errorf("preset_id %q is not a UUID: %v", got.PresetID, err)
	}
}
