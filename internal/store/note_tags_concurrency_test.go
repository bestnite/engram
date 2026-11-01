package store

import (
	"context"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// seedTagRaceNotes creates noteCount tagged notes and returns their ids.
func seedTagRaceNotes(t *testing.T, db *gorm.DB, noteCount int, tags string) []uint64 {
	t.Helper()
	ctx := context.Background()
	owner := seedUsers(t, db, "tag_race_owner")[0]
	deckID := seedDeck(t, db, owner)
	noteTypeID := stockNoteTypeID(t, db, deckID, "basic")
	notes := NewNoteStore(db)
	ids := make([]uint64, 0, noteCount)
	for i := 0; i < noteCount; i++ {
		n := &Note{DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: tags, FieldsJSON: `{"front":"q","back":"a"}`}
		if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
			t.Fatalf("create note %d: %v", i, err)
		}
		ids = append(ids, n.ID)
	}
	return ids
}

// storeTagDatabases returns one migrated database per driver; the SQLite one uses a single connection as Open does,
// since the fix relies on that serialisation and a default pool would hide it.
func storeTagDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	dbs := testDatabases(t)
	for driver, db := range dbs {
		if driver == "sqlite" {
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("sqlite handle: %v", err)
			}
			sqlDB.SetMaxOpenConns(1)
		}
		if err := db.AutoMigrate(AllModels()...); err != nil {
			t.Fatalf("%s AutoMigrate() error = %v", driver, err)
		}
	}
	return dbs
}

// runConcurrently starts both tag operations together and returns their errors. Both are checked, so a swallowed
// write cannot make the result look correct.
func runConcurrently(ops ...func() error) []error {
	start := make(chan struct{})
	errs := make([]error, len(ops))
	var wg sync.WaitGroup
	for i, op := range ops {
		wg.Add(1)
		go func(i int, op func() error) {
			defer wg.Done()
			<-start
			errs[i] = op()
		}(i, op)
	}
	close(start)
	wg.Wait()
	return errs
}

func readNoteTags(t *testing.T, db *gorm.DB, id uint64) []string {
	t.Helper()
	var n Note
	if err := db.First(&n, id).Error; err != nil {
		t.Fatalf("read note %d: %v", id, err)
	}
	tags, err := ParseTags(n.TagsJSON)
	if err != nil {
		t.Fatalf("parse tags of note %d: %v", id, err)
	}
	return tags
}

// TestConcurrentAddTagsKeepsBothAdditions is the union regression: two AddTags calls that both read the same old
// value must not leave the later writer's value alone. The read, merge and write share one locked transaction.
func TestConcurrentAddTagsKeepsBothAdditions(t *testing.T) {
	const rounds = 10
	for driver, db := range storeTagDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			ids := seedTagRaceNotes(t, db, 20, "[]")
			notes := NewNoteStore(db)
			for round := 0; round < rounds; round++ {
				if err := db.Model(&Note{}).Where("id IN ?", ids).Update("tags_json", "[]").Error; err != nil {
					t.Fatalf("reset tags: %v", err)
				}
				errs := runConcurrently(
					func() error { _, err := notes.AddTags(ctx, ids, []string{"a"}, false); return err },
					func() error { _, err := notes.AddTags(ctx, ids, []string{"b"}, false); return err },
				)
				for _, err := range errs {
					if err != nil {
						t.Fatalf("round %d: %v", round, err)
					}
				}
				for _, id := range ids {
					tags := readNoteTags(t, db, id)
					if !containsString(tags, "a") || !containsString(tags, "b") {
						t.Fatalf("round %d note %d tags = %v, want both a and b", round, id, tags)
					}
				}
			}
		})
	}
}

// TestConcurrentRemoveTagsKeepsBothRemovals is the difference regression: two RemoveTags calls must not restore the
// tag the other one removed.
func TestConcurrentRemoveTagsKeepsBothRemovals(t *testing.T) {
	const rounds = 10
	for driver, db := range storeTagDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			ids := seedTagRaceNotes(t, db, 20, `["a","b"]`)
			notes := NewNoteStore(db)
			for round := 0; round < rounds; round++ {
				if err := db.Model(&Note{}).Where("id IN ?", ids).Update("tags_json", `["a","b"]`).Error; err != nil {
					t.Fatalf("reset tags: %v", err)
				}
				errs := runConcurrently(
					func() error { _, err := notes.RemoveTags(ctx, ids, []string{"a"}, false); return err },
					func() error { _, err := notes.RemoveTags(ctx, ids, []string{"b"}, false); return err },
				)
				for i, err := range errs {
					if err != nil {
						t.Fatalf("round %d operation %d: %v", round, i, err)
					}
				}
				for _, id := range ids {
					if tags := readNoteTags(t, db, id); len(tags) != 0 {
						t.Fatalf("round %d note %d tags = %v, want both removals kept", round, id, tags)
					}
				}
			}
		})
	}
}
