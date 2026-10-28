package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestDeckPause runs on SQLite, and on PostgreSQL when TEST_PG_DSN is set: the pause row is written with an ON CONFLICT
// upsert and read through hand-written subqueries, and both databases must agree on the result.
func TestDeckPause(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			t.Run("toggle is per user and idempotent", func(t *testing.T) { assertPauseToggle(t, db) })
			t.Run("default scope leaves out paused decks", func(t *testing.T) { assertPauseScope(t, db) })
			t.Run("deck delete and grant revoke remove the row", func(t *testing.T) { assertPauseCleanup(t, db) })
		})
	}
}

// pauseFixture is one owner deck shared with a reader, plus a second owner deck.
type pauseFixture struct {
	owner, member      uint64
	shared, ownerOther *Deck
}

func seedPauseFixture(t *testing.T, db *gorm.DB, prefix string) pauseFixture {
	t.Helper()
	ctx := context.Background()
	users := seedUsers(t, db, prefix+"_owner", prefix+"_member")
	preset := seedPresetRow(t, db, users[0])
	decks := NewDeckStore(db)
	mk := func(name string) *Deck {
		t.Helper()
		d := &Deck{OwnerUserID: users[0], Name: name, PresetID: preset}
		if err := decks.Create(ctx, d); err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		return d
	}
	f := pauseFixture{owner: users[0], member: users[1], shared: mk(prefix + "-shared"), ownerOther: mk(prefix + "-other")}
	if err := NewGrantStore(db).Grant(ctx, f.shared.ID, f.member, RoleReader, &f.owner); err != nil {
		t.Fatalf("grant: %v", err)
	}
	return f
}

func assertPauseToggle(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	f := seedPauseFixture(t, db, "toggle")
	decks := NewDeckStore(db)
	at := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

	steps := []struct {
		name        string
		paused      bool
		wantChanged bool
	}{
		{"pause", true, true},
		{"pause again", true, false},
		{"resume", false, true},
		{"resume again", false, false},
		{"pause after resume", true, true},
	}
	for _, step := range steps {
		changed, err := decks.SetPaused(ctx, f.member, f.shared.ID, step.paused, at)
		if err != nil {
			t.Fatalf("%s: SetPaused() error = %v", step.name, err)
		}
		if changed != step.wantChanged {
			t.Errorf("%s: changed = %v, want %v", step.name, changed, step.wantChanged)
		}
		got, err := decks.IsPaused(ctx, f.member, f.shared.ID)
		if err != nil {
			t.Fatalf("%s: IsPaused() error = %v", step.name, err)
		}
		if got != step.paused {
			t.Errorf("%s: IsPaused() = %v, want %v", step.name, got, step.paused)
		}
	}
	// The member's pause belongs to the member: the owner still sees the deck as active.
	if paused, _ := decks.IsPaused(ctx, f.owner, f.shared.ID); paused {
		t.Error("owner IsPaused() = true after the member paused, want false")
	}
}

func assertPauseScope(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	f := seedPauseFixture(t, db, "scope")
	decks := NewDeckStore(db)
	if _, err := decks.SetPaused(ctx, f.member, f.shared.ID, true, time.Time{}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := decks.SetPaused(ctx, f.owner, f.ownerOther.ID, true, time.Time{}); err != nil {
		t.Fatalf("pause: %v", err)
	}

	tests := []struct {
		name    string
		userID  uint64
		visible []uint64
		active  []uint64
	}{
		{"owner keeps the deck the member paused", f.owner, []uint64{f.shared.ID, f.ownerOther.ID}, []uint64{f.shared.ID}},
		{"member loses only their paused deck", f.member, []uint64{f.shared.ID}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			visible, err := decks.VisibleIDs(ctx, tc.userID)
			if err != nil {
				t.Fatalf("VisibleIDs() error = %v", err)
			}
			if !sameIDs(visible, tc.visible) {
				t.Errorf("VisibleIDs() = %v, want %v (pausing never hides a deck)", visible, tc.visible)
			}
			active, err := decks.ActiveIDs(ctx, tc.userID)
			if err != nil {
				t.Fatalf("ActiveIDs() error = %v", err)
			}
			if !sameIDs(active, tc.active) {
				t.Errorf("ActiveIDs() = %v, want %v", active, tc.active)
			}
		})
	}
}

func assertPauseCleanup(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	f := seedPauseFixture(t, db, "cleanup")
	decks := NewDeckStore(db)
	for _, p := range []struct{ user, deck uint64 }{
		{f.member, f.shared.ID}, {f.owner, f.shared.ID}, {f.owner, f.ownerOther.ID},
	} {
		if _, err := decks.SetPaused(ctx, p.user, p.deck, true, time.Time{}); err != nil {
			t.Fatalf("pause: %v", err)
		}
	}
	count := func(where string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := db.Model(&DeckPause{}).Where(where, args...).Count(&n).Error; err != nil {
			t.Fatalf("count pauses: %v", err)
		}
		return n
	}

	if err := NewGrantStore(db).Revoke(ctx, f.shared.ID, f.member); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if n := count("deck_id = ? AND user_id = ?", f.shared.ID, f.member); n != 0 {
		t.Errorf("member pause rows after revoke = %d, want 0", n)
	}
	if n := count("deck_id = ? AND user_id = ?", f.shared.ID, f.owner); n != 1 {
		t.Errorf("owner pause rows after revoking the member = %d, want 1", n)
	}

	if err := decks.Delete(ctx, f.owner, f.ownerOther.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if n := count("deck_id = ?", f.ownerOther.ID); n != 0 {
		t.Errorf("pause rows of a deleted deck = %d, want 0", n)
	}
}

func sameIDs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[uint64]bool, len(want))
	for _, id := range want {
		set[id] = true
	}
	for _, id := range got {
		if !set[id] {
			return false
		}
	}
	return true
}
