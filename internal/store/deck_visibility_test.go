package store

import (
	"context"
	"testing"
)

// TestVisibleIDsMatchesListVisibility is the foundation case for the privilege fix: VisibleIDs and ListVisible must see
// the same set of decks (own ∪ granted via deck_grants).
//
// Negative case: a deck someone else created appears neither in the list nor in the id set — being seen requires an explicit grant row,
// and once the grant is revoked it disappears from the next query (same rule as auth.DeckAccess's 'revocation takes effect immediately').
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

			mk := func(owner uint64, name string, presetID uint64) *Deck {
				t.Helper()
				d := &Deck{OwnerUserID: owner, Name: name, PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("create deck %s: %v", name, err)
				}
				return d
			}

			ownOne := mk(me, "own-one", presetMe)
			ownTwo := mk(me, "own-two", presetMe)
			otherOne := mk(other, "other-one", presetOther)
			otherTwo := mk(other, "other-two", presetOther)
			granted := mk(other, "granted", presetOther)

			// grant 'granted' to me: it must appear, while otherOne / otherTwo do not.
			if err := db.Create(&DeckGrant{DeckID: granted.ID, UserID: me, Role: RoleReader}).Error; err != nil {
				t.Fatalf("create grant: %v", err)
			}

			want := map[uint64]bool{
				ownOne.ID:  true,
				ownTwo.ID:  true,
				granted.ID: true,
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
			for _, id := range []uint64{otherOne.ID, otherTwo.ID} {
				if listSet[id] {
					t.Errorf("ListVisible leaked another user's deck %d", id)
				}
				if idSet[id] {
					t.Errorf("VisibleIDs leaked another user's deck %d", id)
				}
			}
			if len(listSet) != len(idSet) {
				t.Errorf("VisibleIDs set size = %d, ListVisible set size = %d; want equal", len(idSet), len(listSet))
			}

			// revoke the grant: under the same set rule it must disappear immediately, with no caching.
			if err := db.Where("deck_id = ? AND user_id = ?", granted.ID, me).Delete(&DeckGrant{}).Error; err != nil {
				t.Fatalf("delete grant: %v", err)
			}
			after, err := decks.VisibleIDs(ctx, me)
			if err != nil {
				t.Fatalf("VisibleIDs() after revoke error = %v", err)
			}
			for _, id := range after {
				if id == granted.ID {
					t.Errorf("VisibleIDs still lists deck %d after the grant was revoked", id)
				}
			}
		})
	}
}
