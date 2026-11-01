package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// TestCreateDeckRefusesAnotherAccountsPreset pins the ownership check on a deck's preset: naming another
// account's preset public id must be refused like an unknown id and must write no deck, while the caller's
// own preset keeps working. Before the check, the foreign preset was resolved, echoed back and written to
// the new deck, so one account's scheduling settings ended up driving another account's deck.
func TestCreateDeckRefusesAnotherAccountsPreset(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var env *testEnv
			if driver == "postgres" {
				db, ok := pgtest.Open(t)
				if !ok {
					t.Skip("TEST_PG_DSN not set")
				}
				env = newTestEnvWithDB(t, db, 1000, 1000)
			} else {
				env = newTestEnv(t, 1000, 1000)
			}
			checkCreateDeckPresetOwner(t, env)
		})
	}
}

func checkCreateDeckPresetOwner(t *testing.T, env *testEnv) {
	t.Helper()
	router := env.router()
	owner := seedUser(t, env.db, "preset_owner", store.RoleUser)
	other := seedUser(t, env.db, "preset_other", store.RoleUser)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)

	own := store.NewPreset(owner.ID, "Mine")
	foreign := store.NewPreset(other.ID, "Foreign")
	for _, p := range []*store.Preset{&own, &foreign} {
		if err := env.db.Create(p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
	}

	t.Run("foreign preset is refused and writes nothing", func(t *testing.T) {
		body := fmt.Sprintf(`{"name":"stolen","preset_id":%q}`, foreign.PublicID)
		code, raw := doJSON(t, router, http.MethodPost, "/api/v1/decks", key.Plaintext, body)
		if code != http.StatusNotFound {
			t.Fatalf("POST /api/v1/decks with another account's preset = %d %s, want 404", code, raw)
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("error body is not JSON: %v (%s)", err, raw)
		}
		if envelope.Error.Code != CodeNotFound {
			t.Errorf("error code = %q, want %q", envelope.Error.Code, CodeNotFound)
		}
		var decks int64
		if err := env.db.Model(&store.Deck{}).Where("owner_user_id = ?", owner.ID).Count(&decks).Error; err != nil {
			t.Fatal(err)
		}
		if decks != 0 {
			t.Errorf("decks owned by the caller = %d, want 0 (a refused create must not write a row)", decks)
		}
		var audits int64
		if err := env.db.Model(&store.AuditLog{}).Where("user_id = ? AND action = ?", owner.ID, store.ActionDeckCreate).Count(&audits).Error; err != nil {
			t.Fatal(err)
		}
		if audits != 0 {
			t.Errorf("deck.create audit entries = %d, want 0 (a refused create must not be audited as a create)", audits)
		}
	})

	t.Run("own preset still creates the deck", func(t *testing.T) {
		body := fmt.Sprintf(`{"name":"mine","preset_id":%q}`, own.PublicID)
		code, raw := doJSON(t, router, http.MethodPost, "/api/v1/decks", key.Plaintext, body)
		if code != http.StatusCreated {
			t.Fatalf("POST /api/v1/decks with the caller's preset = %d %s, want 201", code, raw)
		}
		var created store.Deck
		if err := env.db.Where("owner_user_id = ? AND name = ?", owner.ID, "mine").First(&created).Error; err != nil {
			t.Fatalf("load created deck: %v", err)
		}
		if created.PresetID != own.ID {
			t.Errorf("deck preset = %d, want the caller's preset %d", created.PresetID, own.ID)
		}
	})

	// The MCP create_deck tool resolves the public id through the same service method, so the shared
	// check must refuse a numeric key pointing at another account's preset too.
	if _, err := env.api.CreateDeck(t.Context(), owner, CreateDeckInput{Name: "mcp", PresetID: foreign.ID}); err == nil {
		t.Fatal("CreateDeck accepted another account's preset key")
	} else if se := asServiceError(err); se.Status != http.StatusNotFound || se.Code != CodeNotFound {
		t.Fatalf("CreateDeck error = %+v, want 404 %s", se, CodeNotFound)
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Where("owner_user_id = ? AND name = ?", owner.ID, "mcp").Count(&decks).Error; err != nil {
		t.Fatal(err)
	}
	if decks != 0 {
		t.Errorf("decks named mcp = %d, want 0", decks)
	}
}
