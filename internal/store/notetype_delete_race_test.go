package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
)

// TestConcurrentNoteTypeDeleteAndNoteCreate is the regression for the PostgreSQL race between deleting a note
// type and creating a note against it.
//
// A BEFORE DELETE trigger parks the delete after it has counted zero notes but before the row is removed. A note
// write that starts in that window must not succeed as well: with no row lock both commit, the note's
// note_type_id points at a deleted row, and nothing refuses the write. The invariant asserted here is the one the
// report names — the two operations cannot both succeed leaving an orphan. PostgreSQL only: the SQLite
// deployment holds a single write connection, which serialises them already.
func TestConcurrentNoteTypeDeleteAndNoteCreate(t *testing.T) {
	db, ok := pgtest.Open(t)
	if !ok {
		t.Skip("TEST_PG_DSN not set: the race needs two PostgreSQL connections")
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	owner := seedUsers(t, db, "note_type_race_owner")[0]
	deckID := seedDeck(t, db, owner)
	nt := seedNoteType(t, db, deckID, "Raced")

	// A second connection to the same schema, as a second request would use.
	dialector, ok := db.Dialector.(*postgres.Dialector)
	if !ok {
		t.Fatalf("pgtest.Open returned dialector %T, want *postgres.Dialector", db.Dialector)
	}
	second, err := gorm.Open(postgres.Open(dialector.DSN), &gorm.Config{})
	if err != nil {
		t.Fatalf("open second postgres connection: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := second.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// The trigger is the barrier: it runs after the delete's reference count has seen zero live notes and holds
	// the transaction open long enough for the concurrent note write to arrive.
	if err := db.Exec(`CREATE FUNCTION slow_note_type_delete() RETURNS trigger AS $$
		BEGIN
			PERFORM pg_sleep(1.5);
			RETURN OLD;
		END $$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatalf("create barrier function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER slow_note_type_delete BEFORE DELETE ON note_types
		FOR EACH ROW EXECUTE FUNCTION slow_note_type_delete()`).Error; err != nil {
		t.Fatalf("create barrier trigger: %v", err)
	}

	deleteResult := make(chan error, 1)
	go func() {
		_, err := NewNoteTypeStore(db).Delete(ctx, nt.ID, NoteTypeDeleteOptions{})
		deleteResult <- err
	}()

	// Let the delete reach the trigger before the note write starts.
	time.Sleep(400 * time.Millisecond)

	note := Note{DeckID: deckID, NoteTypeID: &nt.ID, TagsJSON: "[]"}
	_, createErr := NewNoteStore(second).Create(ctx, &note, map[string]any{
		"f-front": "question", "f-back": "answer",
	})
	deleteErr := <-deleteResult

	var typeCount, noteCount int64
	if err := db.Model(&NoteType{}).Where("id = ?", nt.ID).Count(&typeCount).Error; err != nil {
		t.Fatalf("count note types: %v", err)
	}
	if err := db.Unscoped().Model(&Note{}).Where("note_type_id = ?", nt.ID).Count(&noteCount).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}

	if deleteErr == nil && createErr == nil {
		t.Fatalf("delete and note create both succeeded: notes=%d note_types=%d, want the two ordered",
			noteCount, typeCount)
	}
	if typeCount == 0 && noteCount > 0 {
		t.Fatalf("note survives its deleted note type: notes=%d note_types=%d", noteCount, typeCount)
	}
	if createErr == nil {
		// The write won the race: the delete must have been refused by the note it can now see.
		if !errors.Is(deleteErr, ErrNoteTypeInUse) {
			t.Fatalf("delete error = %v after a successful create, want %v", deleteErr, ErrNoteTypeInUse)
		}
	} else if typeCount != 0 {
		t.Fatalf("create failed (%v) but the note type survived; want the delete to have removed it", createErr)
	}
}
