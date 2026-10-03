package store

import (
	"context"
	"testing"
)

// TestVisibleIDsMatchesListVisibility 是越权修复的地基用例：VisibleIDs 与 ListVisible 必须看到
// 同一批卡组（自有 ∪ 被 deck_grants 授权 ∪ 其他用户的 public）。
// 负例：其他用户的 private / unlisted 卡组既不出现在列表里，也不出现在 id 集合里。
func TestVisibleIDsMatchesListVisibility(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "vis_me", "vis_other")
			me, other := users[0], users[1]
			presetMe := seedPresetRow(t, db, me)
			presetOther := seedPresetRow(t, db, other)
			decks := NewDeckStore(db)

			mk := func(owner uint64, name, visibility string, presetID uint64) *Deck {
				t.Helper()
				d := &Deck{OwnerUserID: owner, Name: name, Visibility: visibility, PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("create deck %s: %v", name, err)
				}
				return d
			}

			ownPrivate := mk(me, "own-private", DeckVisibilityPrivate, presetMe)
			ownPublic := mk(me, "own-public", DeckVisibilityPublic, presetMe)
			otherPublic := mk(other, "other-public", DeckVisibilityPublic, presetOther)
			otherPrivate := mk(other, "other-private", DeckVisibilityPrivate, presetOther)
			otherUnlisted := mk(other, "other-unlisted", DeckVisibilityUnlisted, presetOther)
			grantedPrivate := mk(other, "granted-private", DeckVisibilityPrivate, presetOther)

			// 把 granted-private 授权给 me：它必须出现，而 other-private 不出现。
			if err := db.Create(&DeckGrant{DeckID: grantedPrivate.ID, UserID: me, Role: RoleReader}).Error; err != nil {
				t.Fatalf("create grant: %v", err)
			}

			want := map[uint64]bool{
				ownPrivate.ID:     true,
				ownPublic.ID:      true,
				otherPublic.ID:    true,
				grantedPrivate.ID: true,
			}

			visible, err := decks.ListVisible(ctx, me)
			if err != nil {
				t.Fatalf("ListVisible() error = %v", err)
			}
			listSet := map[uint64]bool{}
			for _, d := range visible {
				listSet[d.ID] = true
			}
			ids, err := decks.VisibleIDs(ctx, me)
			if err != nil {
				t.Fatalf("VisibleIDs() error = %v", err)
			}
			idSet := map[uint64]bool{}
			for _, id := range ids {
				idSet[id] = true
			}

			for id := range want {
				if !listSet[id] {
					t.Errorf("ListVisible is missing visible deck %d", id)
				}
				if !idSet[id] {
					t.Errorf("VisibleIDs is missing visible deck %d", id)
				}
			}
			for _, id := range []uint64{otherPrivate.ID, otherUnlisted.ID} {
				if listSet[id] {
					t.Errorf("ListVisible leaked private/unlisted deck %d", id)
				}
				if idSet[id] {
					t.Errorf("VisibleIDs leaked private/unlisted deck %d", id)
				}
			}
			if len(listSet) != len(idSet) {
				t.Errorf("VisibleIDs set size = %d, ListVisible set size = %d; want equal", len(idSet), len(listSet))
			}
		})
	}
}
