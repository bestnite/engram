package store

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// TestStudySettingsArePerUser covers the whole lifecycle of member study settings (both databases):
//   - the owner reads the deck columns; when a grant is written a member gets their own default preset and caps, not the owner's;
//   - a member changing settings touches only their own row, not the deck columns (the owner's settings); the owner changing settings does not affect members;
//   - a member cannot attach someone else's preset (counter-case);
//   - revoking a grant deletes the member row; when the row is missing it falls back to the member's own defaults, not the owner's settings.
func TestStudySettingsArePerUser(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ids := seedUsers(t, db, "ss_owner", "ss_member")
			owner, member := ids[0], ids[1]
			deckID := seedDeck(t, db, owner)
			decks := NewDeckStore(db)
			if err := decks.SetCaps(ctx, owner, deckID, DeckCaps{NewPerDay: 5, ReviewsPerDay: 50}); err != nil {
				t.Fatalf("SetCaps() error = %v", err)
			}
			deck, _ := decks.ByID(ctx, deckID)
			ownerPreset := deck.PresetID

			get := func(user uint64) StudySettings {
				t.Helper()
				d, err := decks.ByID(ctx, deckID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				st, err := decks.StudySettings(ctx, user, d)
				if err != nil {
					t.Fatalf("StudySettings(%d) error = %v", user, err)
				}
				return st
			}

			if err := NewGrantStore(db).Grant(ctx, deckID, member, RoleReader, &owner); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}
			memberDefault := DefaultPreset(mustPresets(t, db, member))
			if memberDefault == nil {
				t.Fatal("granting did not give the member a default preset")
			}
			if got := get(member); got.PresetID != memberDefault.ID || got.Caps != (DeckCaps{NewPerDay: DefaultNewPerDay, ReviewsPerDay: DefaultReviewsPerDay}) {
				t.Fatalf("member settings after grant = %+v, want own default preset %d and default caps", got, memberDefault.ID)
			}
			if got := get(owner); got.PresetID != ownerPreset || got.Caps != (DeckCaps{NewPerDay: 5, ReviewsPerDay: 50}) {
				t.Fatalf("owner settings = %+v, want deck columns", got)
			}

			// a member changes their own settings: the owner's stay unchanged.
			memberCaps := DeckCaps{NewPerDay: 0, ReviewsPerDay: 7}
			d, _ := decks.ByID(ctx, deckID)
			if err := decks.SetStudySettings(ctx, member, d, nil, &memberCaps); err != nil {
				t.Fatalf("member SetStudySettings() error = %v", err)
			}
			if got := get(member).Caps; got != memberCaps {
				t.Errorf("member caps = %+v, want %+v (0 must stay 0)", got, memberCaps)
			}
			if got := get(owner).Caps; got != (DeckCaps{NewPerDay: 5, ReviewsPerDay: 50}) {
				t.Errorf("owner caps changed to %+v by the member's update", got)
			}

			// counter-case: a member attaching the owner's preset is rejected.
			if err := decks.SetStudySettings(ctx, member, d, &ownerPreset, nil); !errors.Is(err, ErrDeckPresetInvalid) {
				t.Errorf("member attaching the owner's preset error = %v, want ErrDeckPresetInvalid", err)
			}

			// the owner changes settings: members' stay unchanged.
			ownerCaps := DeckCaps{NewPerDay: 1, ReviewsPerDay: 1}
			if err := decks.SetStudySettings(ctx, owner, d, nil, &ownerCaps); err != nil {
				t.Fatalf("owner SetStudySettings() error = %v", err)
			}
			if got := get(member).Caps; got != memberCaps {
				t.Errorf("member caps changed to %+v by the owner's update", got)
			}

			// revoking the grant deletes the member row; when missing it falls back to the member's own defaults.
			if err := NewGrantStore(db).Revoke(ctx, deckID, member); err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			var rows int64
			db.Model(&DeckMemberSetting{}).Where("deck_id = ? AND user_id = ?", deckID, member).Count(&rows)
			if rows != 0 {
				t.Errorf("member settings rows after revoke = %d, want 0", rows)
			}
			if got := get(member); got.PresetID != memberDefault.ID {
				t.Errorf("fallback preset = %d, want the member's own default %d", got.PresetID, memberDefault.ID)
			}

			// a preset referenced by a member's settings cannot be deleted.
			extra := Preset{OwnerUserID: member, Name: "mine", DesiredRetention: 0.8, LearningSteps: "1m", RelearningSteps: "10m", MaximumIntervalDays: 100, EnableFuzz: Ptr(true)}
			if err := NewPresetStore(db).Create(ctx, &extra); err != nil {
				t.Fatalf("create preset: %v", err)
			}
			if err := NewGrantStore(db).Grant(ctx, deckID, member, RoleReader, &owner); err != nil {
				t.Fatalf("re-grant: %v", err)
			}
			if err := decks.SetStudySettings(ctx, member, d, &extra.ID, nil); err != nil {
				t.Fatalf("attach own preset: %v", err)
			}
			if err := NewPresetStore(db).Delete(ctx, member, extra.ID); !errors.Is(err, ErrPresetInUse) {
				t.Errorf("deleting a preset used by member settings error = %v, want ErrPresetInUse", err)
			}
		})
	}
}

func mustPresets(t *testing.T, db *gorm.DB, owner uint64) []Preset {
	t.Helper()
	list, err := NewPresetStore(db).ListByOwner(context.Background(), owner)
	if err != nil {
		t.Fatalf("ListByOwner() error = %v", err)
	}
	return list
}
