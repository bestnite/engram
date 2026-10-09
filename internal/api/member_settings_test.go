package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestSharedDeckUsesEachMembersOwnSettings 是 REST 层验收：同一个共享卡组，属主与成员
//   - 在卡组列表里各自看到自己的预设与每日上限；
//   - 复习队列各按自己的新卡上限出卡；
//   - 评分各按自己预设的学习步骤调度（属主有学习步骤，成员的预设没有）。
func TestSharedDeckUsesEachMembersOwnSettings(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	owner := seedUser(t, env.db, "settings-owner", store.RoleUser)
	member := seedUser(t, env.db, "settings-member", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewDeckStore(env.db).SetCaps(ctx, owner.ID, deck.ID, store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 0}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	for i := 0; i < 3; i++ {
		seedKindCard(t, env.db, deck.ID, "basic", fmt.Sprintf(`{"front":"q%d","back":"a"}`, i), "forward")
	}
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, member.ID, store.RoleReader, &owner.ID); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	// 成员把自己的默认预设改成没有学习步骤：评分 Good 直接进入复习态。
	if err := env.db.Model(&store.Preset{}).Where("owner_user_id = ?", member.ID).
		Update("learning_steps", "").Error; err != nil {
		t.Fatalf("edit member preset: %v", err)
	}
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeReview}, nil)
	memberKey := seedKey(t, env.keys, member.ID, []string{store.ScopeRead, store.ScopeReview}, nil)
	router := env.router()

	type deckView struct {
		ID        string `json:"id"`
		PresetID  string `json:"preset_id"`
		NewPerDay int    `json:"new_per_day"`
	}
	listDeck := func(key string) deckView {
		t.Helper()
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/decks", key, "")
		if status != http.StatusOK {
			t.Fatalf("GET /decks status = %d (body %s)", status, raw)
		}
		var body struct {
			Decks []deckView `json:"decks"`
		}
		_ = json.Unmarshal(raw, &body)
		for _, d := range body.Decks {
			if d.ID == deck.PublicID {
				return d
			}
		}
		t.Fatalf("deck %s missing from list %s", deck.PublicID, raw)
		return deckView{}
	}
	ownerView, memberView := listDeck(ownerKey.Plaintext), listDeck(memberKey.Plaintext)
	if ownerView.PresetID == memberView.PresetID {
		t.Errorf("owner and member see the same preset %s, want each their own", ownerView.PresetID)
	}
	if ownerView.NewPerDay != 1 || memberView.NewPerDay != store.DefaultNewPerDay {
		t.Errorf("new_per_day owner=%d member=%d, want 1 and %d", ownerView.NewPerDay, memberView.NewPerDay, store.DefaultNewPerDay)
	}

	due := func(key string) []DueCard {
		t.Helper()
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?deck="+deck.PublicID, key, "")
		if status != http.StatusOK {
			t.Fatalf("GET /review/due status = %d (body %s)", status, raw)
		}
		var body struct {
			Cards []DueCard `json:"cards"`
		}
		_ = json.Unmarshal(raw, &body)
		return body.Cards
	}
	ownerCards, memberCards := due(ownerKey.Plaintext), due(memberKey.Plaintext)
	if len(ownerCards) != 1 || len(memberCards) != 3 {
		t.Fatalf("due cards owner=%d member=%d, want 1 and 3 (each user's own new-card cap)", len(ownerCards), len(memberCards))
	}

	submit := func(key, cardID string) string {
		t.Helper()
		status, raw := doJSON(t, router, http.MethodPost, "/api/v1/review", key,
			fmt.Sprintf(`{"card_id":%q,"rating":3,"expected_version":0}`, cardID))
		if status != http.StatusOK {
			t.Fatalf("POST /review status = %d (body %s)", status, raw)
		}
		var res SubmitReviewResult
		_ = json.Unmarshal(raw, &res)
		return res.State
	}
	if got := submit(ownerKey.Plaintext, ownerCards[0].CardID); got != "learning" {
		t.Errorf("owner state after Good = %q, want learning (owner preset has learning steps)", got)
	}
	if got := submit(memberKey.Plaintext, ownerCards[0].CardID); got != "review" {
		t.Errorf("member state after Good = %q, want review (member preset has no learning steps)", got)
	}
}
