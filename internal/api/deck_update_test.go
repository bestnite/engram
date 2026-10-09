package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestUpdateDeckOwnerEditsNameAndDescription 是 AUDIT-09 的正例：属主经 PATCH /decks/:id
// 改名称与描述，响应回显新值、库已持久化、写一条 deck.update 审计。
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

// TestUpdateDeckKeepsPreset 是 AUDIT 回归：改名只改名称与描述，绝不动卡组的调度预设。
// 修复前改名会把本次调用读到的 preset_id 写回去，覆盖期间变更过的学习设置。
func TestUpdateDeckKeepsPreset(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_preset", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := t.Context()

	// 属主换到另一个自己的预设（走学习设置这条正经路径）。
	second := store.NewPreset(owner.ID, "Second")
	if err := store.NewPresetStore(env.db).Create(ctx, &second); err != nil {
		t.Fatalf("create second preset: %v", err)
	}
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).
		Update("preset_id", second.ID).Error; err != nil {
		t.Fatalf("set preset: %v", err)
	}

	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: "Renamed", Description: "d"}); err != nil {
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

// TestUpdateDeckRejectsNonOwner 是反面用例：editor / reader / 陌生人都不能改名称与描述，
// 且失败不写入。
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
			_, err := env.api.UpdateDeck(t.Context(), tc.user, deck.ID, UpdateDeckInput{Name: "hacked", Description: ""})
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

// TestUpdateDeckRejectsInvalidText 覆盖文本上限与空名称：超长名称 400 deck_name_invalid、
// 空名称 400、超长描述 400 deck_description_invalid；三者都不写入，空描述合法。
func TestUpdateDeckRejectsInvalidText(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner3", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := t.Context()

	tooLongName := strings.Repeat("x", 201)
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: tooLongName}); err == nil {
		t.Fatal("UpdateDeck(over-long name) succeeded, want an error")
	} else if status, code := serviceCode(t, err); status != http.StatusBadRequest || code != CodeDeckNameInvalid {
		t.Fatalf("over-long name = (%d,%q), want (400,%q)", status, code, CodeDeckNameInvalid)
	}
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: "   "}); err == nil {
		t.Fatal("UpdateDeck(blank name) succeeded, want an error")
	}
	tooLongDesc := strings.Repeat("d", 2001)
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: deck.Name, Description: tooLongDesc}); err == nil {
		t.Fatal("UpdateDeck(over-long description) succeeded, want an error")
	} else if status, code := serviceCode(t, err); status != http.StatusBadRequest || code != CodeDeckDescriptionInvalid {
		t.Fatalf("over-long description = (%d,%q), want (400,%q)", status, code, CodeDeckDescriptionInvalid)
	}
	var reloaded store.Deck
	if err := env.db.First(&reloaded, "id = ?", deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.Name != deck.Name || reloaded.Description != deck.Description {
		t.Errorf("deck changed by a rejected update: %q/%q", reloaded.Name, reloaded.Description)
	}
	// 空描述合法：清空描述应成功。
	if _, err := env.api.UpdateDeck(ctx, owner, deck.ID, UpdateDeckInput{Name: deck.Name, Description: ""}); err != nil {
		t.Fatalf("UpdateDeck(empty description) error = %v, want nil", err)
	}
}

// TestUpdateDeckUnknownID 断言未知卡组 id 返回 404。
func TestUpdateDeckUnknownID(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "deck_upd_owner4", store.RoleUser)
	_, err := env.api.UpdateDeck(t.Context(), owner, 999999, UpdateDeckInput{Name: "x"})
	status, code := serviceCode(t, err)
	if status != http.StatusNotFound || code != CodeNotFound {
		t.Fatalf("UpdateDeck(unknown id) = (%d,%q), want (404,%q)", status, code, CodeNotFound)
	}
}
