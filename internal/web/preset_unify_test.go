package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDefaultPresetSingleAcrossWebAndREST is the regression for "one user ends up with two default presets".
//
// Before the fix: web created the default preset with a localized name (the zh-CN text for "Default"), while REST looked it up / created it with the literal "Default",
// so the two entries gave the same logical preset different names, and a user who created a deck through web and then through REST got one row from each.
// After the fix both entries share store.DefaultPresetName, so one user has exactly one default preset.
//
// Both paths really run in this case: web goes through the form handler (default language zh-CN), REST through api.CreateDeck
// (the service method REST/MCP/CLI share).
func TestDefaultPresetSingleAcrossWebAndREST(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	// Create a deck through web: goes through the SPA's same-origin JSON deck endpoint (the SSR POST /decks form has been deleted).
	rec := postJSONWithSession(srv, "/api/v1/decks", map[string]any{
		"name":        "web deck",
		"description": "",
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("web create deck = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// Create a deck through REST: reuses the same api instance.
	owner, err := store.NewUserStore(db).ByID(ctx, ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if _, err := srv.api.CreateDeck(ctx, owner, api.CreateDeckInput{Name: "rest deck"}); err != nil {
		t.Fatalf("REST create deck: %v", err)
	}

	presets, err := store.NewPresetStore(db).ListByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("default presets after web+REST deck creation = %d, want exactly 1", len(presets))
	}
	if presets[0].Name != store.DefaultPresetName {
		t.Errorf("default preset name = %q, want %q", presets[0].Name, store.DefaultPresetName)
	}

	// Both decks were created, confirming the case really went down both deck-creation paths.
	var decks int64
	if err := db.Model(&store.Deck{}).Where("owner_user_id = ?", ownerID).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 2 {
		t.Errorf("decks = %d, want 2 (one from web, one from REST)", decks)
	}
}
