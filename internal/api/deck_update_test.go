package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// strPtr returns a string pointer for building PATCH-semantics inputs (nil = this field is not being changed).
func strPtr(s string) *string { return &s }

// TestUpdateDeckOwnerEditsNameAndDescription is the positive case for AUDIT-09: the owner PATCHes /decks/:id
// to change name and description; the response echoes the new values, the database is persisted, and one deck.update audit row is written.
func TestUpdateDeckOwnerEditsNameAndDescription(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite, store.ScopeRead}, nil)
	router := env.router()

	status, raw := doJSON(t, router, http.MethodPatch, "/api/v1/decks/"+deck.PublicID, key.Plaintext,
		`{"name":"Renamed","description":"new description"}`)
	if status != http.StatusOK {
		t.Fatalf("update deck = %d, want 200 (body %s)", status, raw)
	}
	var resp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, raw)
	}
	if resp.Name != "Renamed" || resp.Description != "new description" {
		t.Errorf("response = %+v, want the new name and description", resp)
	}
	var reloaded store.Deck
	if err := env.db.First(&reloaded, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.Name != "Renamed" || reloaded.Description != "new description" {
		t.Errorf("persisted deck = %q/%q, want the new values", reloaded.Name, reloaded.Description)
	}
	var audits int64
	env.db.Model(&store.AuditLog{}).Where("action = ?", store.ActionDeckUpdate).Count(&audits)
	if audits != 1 {
		t.Errorf("deck.update audit rows = %d, want 1", audits)
	}
}

// TestUpdateDeckKeepsPreset is an AUDIT regression: renaming changes only name and description and never touches the deck's scheduling preset.
// Before the fix, a rename wrote back the preset_id read during that call, overwriting study settings changed in the meantime.
func TestUpdateDeckKeepsPreset(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_preset", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := t.Context()

	// The owner switches to another of their own presets (through the proper study-settings path).
	second := store.NewPreset(owner.ID, "Second")
	if err := store.NewPresetStore(env.db).Create(ctx, &second); err != nil {
		t.Fatalf("create second preset: %v", err)
	}
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).
		Update("preset_id", second.ID).Error; err != nil {
		t.Fatalf("set preset: %v", err)
	}

	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr("Renamed"), Description: strPtr("d")}); err != nil {
		t.Fatalf("UpdateDeck error = %v", err)
	}
	var reloaded store.Deck
	if err := env.db.First(&reloaded, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", reloaded.Name)
	}
	if reloaded.PresetID != second.ID {
		t.Errorf("preset_id = %d, want %d (a rename must not touch the scheduling preset)", reloaded.PresetID, second.ID)
	}
}

// TestUpdateDeckRejectsNonOwner is the negative case: editor / reader / strangers cannot change name and description,
// and a failure writes nothing.
func TestUpdateDeckRejectsNonOwner(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner2", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)

	grants := store.NewGrantStore(env.db)
	editor := seedUser(t, env.db, "deck_upd_editor", store.RoleUser)
	reader := seedUser(t, env.db, "deck_upd_reader", store.RoleUser)
	for _, m := range []struct {
		u    *store.User
		role string
	}{{editor, store.RoleEditor}, {reader, store.RoleReader}} {
		if err := grants.Grant(t.Context(), deck.ID, m.u.ID, m.role, store.Ptr(owner.ID)); err != nil {
			t.Fatalf("grant %s: %v", m.role, err)
		}
	}

	for _, tc := range []struct {
		name string
		user *store.User
	}{
		{"editor", editor},
		{"reader", reader},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.api.UpdateDeck(t.Context(), tc.user, deck.ID, UpdateDeckInput{Name: strPtr("hacked")})
			status, code := serviceCode(t, err)
			if status != http.StatusForbidden || code != CodeForbidden {
				t.Fatalf("UpdateDeck(%s) = (%d,%q), want (403,%q)", tc.name, status, code, CodeForbidden)
			}
		})
	}
	var reloaded store.Deck
	if err := env.db.First(&reloaded, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.Name != deck.Name {
		t.Errorf("deck name = %q, want the original (a denied update must not write)", reloaded.Name)
	}
}

// TestUpdateDeckRejectsInvalidText covers text caps and empty names: an over-long name 400 deck_name_invalid,
// an empty name 400, an over-long description 400 deck_description_invalid; none of the three writes, and an empty description is legal.
func TestUpdateDeckRejectsInvalidText(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner3", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := t.Context()

	tooLongName := strings.Repeat("x", 201)
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr(tooLongName)}); err == nil {
		t.Fatal("UpdateDeck(over-long name) succeeded, want an error")
	} else if status, code := serviceCode(t, err); status != http.StatusBadRequest || code != CodeDeckNameInvalid {
		t.Fatalf("over-long name = (%d,%q), want (400,%q)", status, code, CodeDeckNameInvalid)
	}
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr("   ")}); err == nil {
		t.Fatal("UpdateDeck(blank name) succeeded, want an error")
	}
	tooLongDesc := strings.Repeat("d", 2001)
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr(deck.Name), Description: strPtr(tooLongDesc)}); err == nil {
		t.Fatal("UpdateDeck(over-long description) succeeded, want an error")
	} else if status, code := serviceCode(t, err); status != http.StatusBadRequest || code != CodeDeckDescriptionInvalid {
		t.Fatalf("over-long description = (%d,%q), want (400,%q)", status, code, CodeDeckDescriptionInvalid)
	}
	// Neither field given: nothing to write, 400 (not a silent success).
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{}); err == nil {
		t.Fatal("UpdateDeck(no fields) succeeded, want an error")
	} else if status, code := serviceCode(t, err); status != http.StatusBadRequest || code != CodeInvalidRequest {
		t.Fatalf("no fields = (%d,%q), want (400,%q)", status, code, CodeInvalidRequest)
	}
	var reloaded store.Deck
	if err := env.db.First(&reloaded, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.Name != deck.Name || reloaded.Description != deck.Description {
		t.Errorf("deck changed by a rejected update: %q/%q", reloaded.Name, reloaded.Description)
	}
	// An empty description is legal: an explicit empty string should clear the description.
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr(deck.Name), Description: strPtr("")}); err != nil {
		t.Fatalf("UpdateDeck(empty description) error = %v, want nil", err)
	}
}

// TestUpdateDeckPartialUpdateKeepsOmittedFields is an AUDIT regression: PATCH changes only the fields given.
// Before the fix the body fields were plain strings, so an omitted description was written as an empty string, wiping the original one.
func TestUpdateDeckPartialUpdateKeepsOmittedFields(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_partial", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).
		Updates(map[string]any{"name": "Original", "description": "keep me"}).Error; err != nil {
		t.Fatalf("seed deck text: %v", err)
	}
	ctx := t.Context()

	// ① Name only: the description stays unchanged.
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: strPtr("Renamed")}); err != nil {
		t.Fatalf("UpdateDeck(name only) error = %v", err)
	}
	var afterName store.Deck
	if err := env.db.First(&afterName, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if afterName.Name != "Renamed" || afterName.Description != "keep me" {
		t.Fatalf("after name-only update = %q/%q, want Renamed/keep me", afterName.Name, afterName.Description)
	}

	// ② Description only: the name stays unchanged.
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Description: strPtr("new desc")}); err != nil {
		t.Fatalf("UpdateDeck(description only) error = %v", err)
	}
	var afterDesc store.Deck
	if err := env.db.First(&afterDesc, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if afterDesc.Name != "Renamed" || afterDesc.Description != "new desc" {
		t.Fatalf("after description-only update = %q/%q, want Renamed/new desc", afterDesc.Name, afterDesc.Description)
	}
}

// TestUpdateDeckHTTPPartialBodyKeepsDescription is the HTTP-level acceptance for the same regression: PATCH carrying only name.
func TestUpdateDeckHTTPPartialBodyKeepsDescription(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_http", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).
		Updates(map[string]any{"name": "Original", "description": "keep me"}).Error; err != nil {
		t.Fatalf("seed deck text: %v", err)
	}
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)

	status, raw := doJSON(t, env.router(), http.MethodPatch, "/api/v1/decks/"+deck.PublicID, key.Plaintext, `{"name":"Renamed"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH name-only = %d, want 200 (body %s)", status, raw)
	}
	var resp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, raw)
	}
	if resp.Name != "Renamed" || resp.Description != "keep me" {
		t.Errorf("response = %q/%q, want Renamed/keep me (omitted description must stay)", resp.Name, resp.Description)
	}
}

// TestUpdateDeckUnknownID asserts an unknown deck id returns 404.
func TestUpdateDeckUnknownID(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner4", store.RoleUser)
	_, err := env.api.UpdateDeck(t.Context(), owner, 999999, UpdateDeckInput{Name: strPtr("x")})
	status, code := serviceCode(t, err)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Fatalf("UpdateDeck(unknown id) = (%d,%q), want (404,%q)", status, code, CodeNotFound)
	}
}
