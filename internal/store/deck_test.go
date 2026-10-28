package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// seedUsers creates a few users for the test and returns their primary keys; usernames and emails are built from the prefix the caller supplies, so unique constraints do not collide.
func seedUsers(t *testing.T, db *gorm.DB, names ...string) []uint64 {
	t.Helper()
	now := time.Now().UTC()
	ids := make([]uint64, 0, len(names))
	for _, name := range names {
		u := User{Username: name, Email: name + "@example.com", DisplayName: name, Role: RoleUser,
			Status: StatusActive, Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: now}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		ids = append(ids, u.ID)
	}
	return ids
}

// seedPresetRow writes one preset row directly, for a deck's preset_id to reference; all fields are set to avoid default-value traps.
func seedPresetRow(t *testing.T, db *gorm.DB, ownerID uint64) uint64 {
	t.Helper()
	now := time.Now().UTC()
	p := Preset{OwnerUserID: ownerID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: Ptr(true), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return p.ID
}

func TestDeckStoreCRUD(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "deck_owner_a", "deck_owner_b")
			ownerA, ownerB := users[0], users[1]
			presetID := seedPresetRow(t, db, ownerA)
			presetB := seedPresetRow(t, db, ownerB)
			decks := NewDeckStore(db)

			// create: ownership is given by OwnerUserID.
			d := &Deck{OwnerUserID: ownerA, Name: "Deck A", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if d.ID == 0 {
				t.Fatal("Create() did not populate ID")
			}
			if d.CreatedAt.IsZero() {
				t.Error("Create() did not set CreatedAt")
			}

			// read: ByID fetches the same row back.
			got, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.Name != "Deck A" || got.OwnerUserID != ownerA {
				t.Errorf("ByID() = %+v, want name Deck A owner %d", got, ownerA)
			}

			// update: the owner changes only name and description; the preset is not a writable field (switching presets goes through SetStudySettings).
			got.Name = "Deck A renamed"
			got.Description = "desc"
			got.PresetID = presetB
			if err := decks.Update(ctx, ownerA, got.ID, &got.Name, &got.Description); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			reloaded, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if reloaded.Name != "Deck A renamed" || reloaded.Description != "desc" {
				t.Errorf("after Update() = %+v, want renamed/desc", reloaded)
			}
			if reloaded.PresetID != d.PresetID {
				t.Errorf("after Update() preset = %d, want %d (a metadata update must not change the preset)", reloaded.PresetID, d.PresetID)
			}

			// delete: a non-owner deletion is rejected.
			if err := decks.Delete(ctx, ownerB, d.ID); !errors.Is(err, ErrNotOwner) {
				t.Errorf("Delete() by non-owner error = %v, want ErrNotOwner", err)
			}
			// the owner deletes successfully, after which ByID reports record not found.
			if err := decks.Delete(ctx, ownerA, d.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := decks.ByID(ctx, d.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByID() after Delete() error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}

// TestDeckNonOwnerCannotModifyBeforeGrants is an acceptance case:
// with no rows in deck_grants yet, any non-owner modification of a deck must be rejected — this is the foundation.
func TestDeckNonOwnerCannotModifyBeforeGrants(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "grant_owner", "grant_stranger")
			owner, stranger := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			d := &Deck{OwnerUserID: owner, Name: "Shared", PresetID: presetID}
			if err := decks.Create(ctx, d); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			// confirm the premise: the grants table has no rows at this moment.
			var grants int64
			if err := db.Model(&DeckGrant{}).Where("deck_id = ?", d.ID).Count(&grants).Error; err != nil {
				t.Fatalf("count grants: %v", err)
			}
			if grants != 0 {
				t.Fatalf("precondition failed: deck %d already has %d grants", d.ID, grants)
			}

			// a non-owner changing settings is rejected.
			attempt := *d
			attempt.Name = "Hijacked"
			if err := decks.Update(ctx, stranger, attempt.ID, &attempt.Name, &attempt.Description); !errors.Is(err, ErrNotOwner) {
				t.Errorf("non-owner Update() error = %v, want ErrNotOwner", err)
			}

			// the row in the database must be left untouched.
			after, err := decks.ByID(ctx, d.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if after.Name != "Shared" {
				t.Errorf("deck was modified by a non-owner: %+v", after)
			}
		})
	}
}

// TestDeckCreateValidation covers the two rejection paths, missing preset and empty name.
func TestDeckCreateValidation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_validate_owner")[0]
			decks := NewDeckStore(db)

			if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "", PresetID: 1}); !errors.Is(err, ErrDeckNameRequired) {
				t.Errorf("Create(empty name) error = %v, want ErrDeckNameRequired", err)
			}
			if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "NoPreset"}); !errors.Is(err, ErrDeckPresetRequired) {
				t.Errorf("Create(no preset) error = %v, want ErrDeckPresetRequired", err)
			}
		})
	}
}
