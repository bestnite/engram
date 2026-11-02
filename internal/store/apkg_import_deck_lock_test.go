package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
)

// waitForDeckRowLockWait polls PostgreSQL's activity view until the second import's backend is
// waiting on the target deck's row lock. That UPDATE is the serialisation point the naming
// decision needs; without it the second import would instead block later, on the media_names row.
// It reports whether the wait was observed.
func waitForDeckRowLockWait(t *testing.T, db *gorm.DB) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		err := db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			  AND query LIKE '%UPDATE decks SET name = name%'`).Scan(&waiting).Error
		if err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestAnkiImportSerialisesConcurrentIntoDeckMediaNames imports two packages that both carry a file
// named placeholder.png with different bytes into one deck, with the first import's transaction
// still open while the second one starts.
//
// Without the deck lock both imports decide against the same snapshot: both keep the name, and the
// second registration repoints placeholder.png at its own bytes, so the first import's note renders
// the second package's file. With the lock the second import waits, sees the committed name and
// renames its file, so each name resolves to the bytes its own package carried.
func TestAnkiImportSerialisesConcurrentIntoDeckMediaNames(t *testing.T) {
	ctx := context.Background()
	db, ok := pgtest.Open(t)
	if !ok {
		t.Skip("TEST_PG_DSN not set: the interleaving needs two PostgreSQL connections")
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	owner := seedUsers(t, db, "anki_import_deck_lock")[0]
	presetID := seedPresetRow(t, db, owner)
	deck := Deck{OwnerUserID: owner, Name: "concurrent imports", PresetID: presetID, Description: "target"}
	if err := NewDeckStore(db).Create(ctx, &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}

	bytesA := []byte("\x89PNG\r\n\x1a\nfirst package")
	bytesB := []byte("\x89PNG\r\n\x1a\nsecond package")
	shaA, shaB := shaOf(bytesA), shaOf(bytesB)
	if shaA == shaB {
		t.Fatal("the two packages must carry different bytes")
	}
	rawA := ankiFixtureWithMedia(t, "stock.anki2.apkg",
		map[string][]byte{"0": bytesA}, map[string]string{"0": "placeholder.png"})
	rawB := ankiFixtureWithMedia(t, "stock.anki2.apkg",
		map[string][]byte{"0": bytesB}, map[string]string{"0": "placeholder.png"})

	limits := DefaultPackageImportLimits()
	store := NewDeckStore(db)
	ctx = WithActor(ctx, owner)
	prepare := func(raw []byte, root string, onConflict string) *importPlacementValue {
		t.Helper()
		opts := PackageImportOptions{
			Target:     fmt.Sprintf("into_deck:%d", deck.ID),
			MediaRoot:  root,
			OnConflict: onConflict,
			Now:        func() time.Time { return time.Now().UTC() },
		}
		placement, err := store.prepareDeckImport(ctx, owner, bytes.NewReader(raw), int64(len(raw)), limits, opts, "into_deck", deck.ID)
		if err != nil {
			t.Fatalf("prepare import: %v", err)
		}
		return placement
	}
	// B skips notes that already exist so its import leaves A's note fields alone; the media naming
	// decision, which is what this test pins, still runs before the note loop.
	placementA := prepare(rawA, t.TempDir(), "update")
	placementB := prepare(rawB, t.TempDir(), "skip")

	run := func(placement *importPlacementValue, tx *gorm.DB) error {
		return store.importPackageInTx(ctx, tx, placement.actorUserID, placement.pkg, placement.files,
			placement.placed, &placement.plan, placement.targetKind, placement.targetDeckID,
			placement.opts, placement.report)
	}

	txA := db.Begin()
	if txA.Error != nil {
		t.Fatalf("begin first import: %v", txA.Error)
	}
	defer txA.Rollback()
	if err := run(placementA, txA); err != nil {
		txA.Rollback()
		t.Fatalf("first import: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		txB := db.Begin()
		if txB.Error != nil {
			done <- txB.Error
			return
		}
		if err := run(placementB, txB); err != nil {
			txB.Rollback()
			done <- err
			return
		}
		done <- txB.Commit().Error
	}()

	if !waitForDeckRowLockWait(t, db) {
		t.Errorf("the second import did not wait for the target deck's row lock")
	}
	if err := txA.Commit().Error; err != nil {
		t.Fatalf("commit first import: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second import: %v", err)
	}

	rows, err := NewMediaNameStore(db).ListByDeck(ctx, deck.ID)
	if err != nil {
		t.Fatalf("list media names: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("media_names rows = %d (%+v), want 2: one name per package's bytes", len(rows), rows)
	}
	bySha := make(map[string]string, len(rows))
	for _, row := range rows {
		bySha[row.Sha256] = row.Name
	}
	if bySha[shaA] != "placeholder.png" {
		t.Errorf("first package's bytes are registered as %q, want the uncontested name %q", bySha[shaA], "placeholder.png")
	}
	if bySha[shaB] == "" || bySha[shaB] == "placeholder.png" {
		t.Errorf("second package's bytes are registered as %q, want a renamed file", bySha[shaB])
	}

	// The first import's note still resolves through the name it was written with, and that name
	// still points at the first package's bytes.
	var note Note
	if err := db.WithContext(ctx).Where("deck_id = ? AND external_ref = ?", deck.ID, "anki:stock-image-occlusion-one").
		First(&note).Error; err != nil {
		t.Fatalf("load first import's note: %v", err)
	}
	if !strings.Contains(note.FieldsJSON, "placeholder.png") {
		t.Errorf("first import's note no longer references placeholder.png: %s", note.FieldsJSON)
	}

	// The second package's references were rewritten to the renamed file, not left pointing at the
	// first package's bytes.
	secondRef := bySha[shaB]
	for i := range placementB.pkg.Notes {
		if strings.Contains(strings.Join(placementB.pkg.Notes[i].Fields, "\x00"), secondRef) {
			return
		}
	}
	t.Errorf("second package's in-memory notes were not rewritten to %q", secondRef)
}
