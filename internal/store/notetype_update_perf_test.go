package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/notetype"
)

// sqlTraceLogger records the SQL text of every statement GORM executes, so a test can
// assert which tables a store call touched without depending on a wall-clock threshold.
type sqlTraceLogger struct {
	gormlogger.Interface
	mu         sync.Mutex
	statements []string
}

func newSQLTraceLogger() *sqlTraceLogger {
	return &sqlTraceLogger{Interface: gormlogger.Discard}
}

func (l *sqlTraceLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }

func (l *sqlTraceLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	l.mu.Lock()
	l.statements = append(l.statements, sql)
	l.mu.Unlock()
}

func (l *sqlTraceLogger) reset() {
	l.mu.Lock()
	l.statements = nil
	l.mu.Unlock()
}

func (l *sqlTraceLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.statements...)
}

// sqlTouchesTable reports whether one statement reads or writes the named table. It handles
// both quoting styles: SQLite uses backticks and PostgreSQL uses double quotes.
func sqlTouchesTable(statement, table string) bool {
	lower := strings.ToLower(statement)
	for _, verb := range []string{"from ", "join ", "into ", "update ", "delete from "} {
		for _, quote := range []string{"`", "\""} {
			if strings.Contains(lower, verb+quote+table+quote) {
				return true
			}
		}
		if strings.Contains(lower, verb+table+" ") || strings.HasSuffix(lower, verb+table) {
			return true
		}
	}
	return false
}

func countStatements(statements []string, match func(string) bool) int {
	count := 0
	for _, statement := range statements {
		if match(statement) {
			count++
		}
	}
	return count
}

func isCardSelect(statement string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(statement)), "select") &&
		sqlTouchesTable(statement, "cards")
}

// seedNotesForPerf creates count notes of one note type through the note write path, so every
// note holds the cards and derived rows a real note holds.
func seedNotesForPerf(t *testing.T, db *gorm.DB, noteTypeID, deckID uint64, count int) {
	t.Helper()
	notes := NewNoteStore(db)
	for i := 0; i < count; i++ {
		note := Note{DeckID: deckID, NoteTypeID: &noteTypeID}
		if _, err := notes.Create(context.Background(), &note, map[string]any{"Front": "Q", "Back": "A"}); err != nil {
			t.Fatalf("create note %d: %v", i, err)
		}
	}
}

// TestNoteTypeUpdateLightChangeDoesNotLoadNotes proves a save that changes only presentation
// data performs the same number of statements whatever the note count: it never loads the
// notes and never queries the cards. Only the name, CSS, LaTeX settings and sort field change.
func TestNoteTypeUpdateLightChangeDoesNotLoadNotes(t *testing.T) {
	db := testSQLiteDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	owner := seedUsers(t, db, "note_type_light_owner")[0]
	deckID := seedDeck(t, db, owner)
	capture := newSQLTraceLogger()
	session := db.Session(&gorm.Session{Logger: capture})
	types := NewNoteTypeStore(session)

	// Each size gets its own note type in the same database, so the only difference between the
	// measured updates is how many notes the type backs.
	commitCounts := make(map[int]int, 2)
	dryRunCounts := make(map[int]int, 2)
	for _, size := range []int{1, 40} {
		nt := NoteType{DeckID: deckID, Name: "Light"}
		if err := types.Create(ctx, &nt, noteTypeConfig("Card 1")); err != nil {
			t.Fatalf("create note type with %d notes: %v", size, err)
		}
		seedNotesForPerf(t, db, nt.ID, deckID, size)

		cfg := noteTypeConfig("Card 1")
		cfg.CSS = ".card { color: rebeccapurple; }"
		cfg.LaTeXPre = "\\documentclass{article}"
		cfg.LaTeXPost = "\\end{document}"
		cfg.SortFieldID = "f-back"
		update := nt
		update.Name = "Light renamed"

		// A dry run takes the same path and rolls back, so it must also avoid the note walk.
		capture.reset()
		if _, err := types.Update(ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0, DryRun: true}); err != nil {
			t.Fatalf("dry-run Update(%d notes) error = %v", size, err)
		}
		dryStatements := capture.snapshot()
		assertLightUpdateStatements(t, size, "dry run", dryStatements)

		capture.reset()
		impact, err := types.Update(ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0})
		if err != nil {
			t.Fatalf("Update(%d notes) error = %v", size, err)
		}
		statements := capture.snapshot()
		assertLightUpdateStatements(t, size, "commit", statements)
		if impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
			t.Errorf("impact with %d notes = added %d removed %d, want 0/0", size, impact.CardsAdded, impact.CardsRemoved)
		}
		t.Logf("light update: notes=%d dry_run_statements=%d commit_statements=%d card_selects=%d",
			size, len(dryStatements), len(statements), countStatements(statements, isCardSelect))
		commitCounts[size] = len(statements)
		dryRunCounts[size] = len(dryStatements)
	}
	if commitCounts[1] != commitCounts[40] {
		t.Errorf("statements for a light commit: %d with 1 note, %d with 40 notes; want the same count",
			commitCounts[1], commitCounts[40])
	}
	if dryRunCounts[1] != dryRunCounts[40] {
		t.Errorf("statements for a light dry run: %d with 1 note, %d with 40 notes; want the same count",
			dryRunCounts[1], dryRunCounts[40])
	}
}

// assertLightUpdateStatements checks a presentation-only update leaves the note and card tables
// untouched, whether it committed or rolled back.
func assertLightUpdateStatements(t *testing.T, size int, mode string, statements []string) {
	t.Helper()
	if got := countStatements(statements, isCardSelect); got != 0 {
		t.Errorf("%s with %d notes ran %d card SELECT(s), want 0", mode, size, got)
	}
	if got := countStatements(statements, func(s string) bool { return sqlTouchesTable(s, "notes") }); got != 0 {
		t.Errorf("%s with %d notes touched the notes table %d time(s), want 0", mode, size, got)
	}
	if got := countStatements(statements, func(s string) bool {
		return sqlTouchesTable(s, "media_notes")
	}); got != 0 {
		t.Errorf("%s with %d notes touched media_notes %d time(s), want 0", mode, size, got)
	}
}

// TestNoteTypeUpdateTemplateChangeBatchesCardReads proves that when a template change keeps the
// generated card set as it was, the update reads the stored cards in batches instead of one
// SELECT per note: the query count is ceil(notes/500), not notes, and no card row changes.
func TestNoteTypeUpdateTemplateChangeBatchesCardReads(t *testing.T) {
	db := testSQLiteDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	owner := seedUsers(t, db, "note_type_batch_owner")[0]
	deckID := seedDeck(t, db, owner)
	capture := newSQLTraceLogger()
	session := db.Session(&gorm.Session{Logger: capture})
	types := NewNoteTypeStore(session)

	for _, size := range []int{20, 600} {
		nt := NoteType{DeckID: deckID, Name: "Batch"}
		if err := types.Create(ctx, &nt, noteTypeConfig("Card 1")); err != nil {
			t.Fatalf("create note type with %d notes: %v", size, err)
		}
		seedNotesForPerf(t, db, nt.ID, deckID, size)

		var before []Card
		if err := db.Where("note_id IN (SELECT id FROM notes WHERE note_type_id = ?)", nt.ID).
			Order("id ASC").Find(&before).Error; err != nil {
			t.Fatalf("list cards before update: %v", err)
		}

		// A static wrapper around the front is the common front-edit case: notetype.GenerateCards
		// returns the same card set for every note. The sync still has to inspect every note's
		// stored cards, because that is what restores a soft-deleted row.
		cfg := noteTypeConfig("Card 1")
		cfg.Templates[0].QFormat = `<div class="wrapped">{{Front}}</div>`
		update := nt

		capture.reset()
		if _, err := types.Update(ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0, DryRun: true, ConfirmCardRemoval: true}); err != nil {
			t.Fatalf("dry-run Update(%d notes) error = %v", size, err)
		}
		dryStatements := capture.snapshot()
		if got, want := countStatements(dryStatements, isCardSelect), (size+499)/500; got != want {
			t.Errorf("dry-run card SELECTs with %d notes = %d, want %d batched read(s)", size, got, want)
		}

		capture.reset()
		impact, err := types.Update(ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0, ConfirmCardRemoval: true})
		if err != nil {
			t.Fatalf("Update(%d notes) error = %v", size, err)
		}
		statements := capture.snapshot()
		if impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
			t.Errorf("impact with %d notes = added %d removed %d, want 0/0", size, impact.CardsAdded, impact.CardsRemoved)
		}
		if got, want := countStatements(statements, isCardSelect), (size+499)/500; got != want {
			t.Errorf("card SELECTs with %d notes = %d, want %d batched read(s)", size, got, want)
		}
		if got := countStatements(statements, func(s string) bool { return sqlTouchesTable(s, "notes") }); got != 1 {
			t.Errorf("note loads with %d notes = %d, want 1", size, got)
		}
		if got := countStatements(statements, func(s string) bool {
			return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "update") && sqlTouchesTable(s, "notes")
		}); got != 0 {
			t.Errorf("a front-format-only update rewrote notes %d time(s), want 0", got)
		}
		if got := countStatements(statements, func(s string) bool {
			return sqlTouchesTable(s, "media_notes")
		}); got != 0 {
			t.Errorf("a front-format-only update touched media_notes %d time(s), want 0", got)
		}
		t.Logf("template update: notes=%d dry_run_card_selects=%d commit_card_selects=%d note_loads=%d", size,
			countStatements(dryStatements, isCardSelect),
			countStatements(statements, isCardSelect),
			countStatements(statements, func(s string) bool { return sqlTouchesTable(s, "notes") }))

		var after []Card
		if err := db.Where("note_id IN (SELECT id FROM notes WHERE note_type_id = ?)", nt.ID).
			Order("id ASC").Find(&after).Error; err != nil {
			t.Fatalf("list cards after update: %v", err)
		}
		if len(before) != len(after) || len(after) != size {
			t.Fatalf("cards with %d notes: before %d after %d, want %d each", size, len(before), len(after), size)
		}
		for i := range before {
			if before[i].ID != after[i].ID {
				t.Fatalf("card %d changed id from %d to %d on a front-format-only update",
					i, before[i].ID, after[i].ID)
			}
		}
	}
}

// lightPathFixture is one note type with live notes, a visible card carrying a card_state and a
// review, a soft-deleted card that no presentation-only save may restore, and a note field that
// references stored media so media_notes holds a row.
type lightPathFixture struct {
	ctx               context.Context
	db                *gorm.DB
	nt                NoteType
	noteID            uint64
	visibleCardID     uint64
	softDeletedCardID uint64
}

func newLightPathFixture(t *testing.T, initialCSS string) *lightPathFixture {
	t.Helper()
	db := testSQLiteDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	owner := seedUsers(t, db, "note_type_light_path")[0]
	deckID := seedDeck(t, db, owner)
	types := NewNoteTypeStore(db)
	nt := NoteType{DeckID: deckID, Name: "Light path"}
	cfg := noteTypeConfig("Card 1")
	cfg.CSS = initialCSS
	// The second template stays declared and produces a card for this note (Back is non-empty),
	// so an unconditional card sync would restore the row this fixture soft-deletes below. That is
	// what makes the presentation-only assertions prove the optimization boundary rather than an
	// undeclared template that no sync would ever restore.
	cfg.Templates = append(cfg.Templates, notetype.Template{
		ID: "t-second", Name: "Card 2", QFormat: "{{Back}}", AFormat: "{{Front}}",
	})
	if err := types.Create(ctx, &nt, cfg); err != nil {
		t.Fatalf("create note type: %v", err)
	}
	sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("light-path"))
	note := Note{DeckID: deckID, NoteTypeID: &nt.ID}
	if _, err := NewNoteStore(db).Create(ctx, &note, map[string]any{
		"Front": `<img src="` + sha + `.png">`,
		"Back":  "A",
	}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	var visible Card
	if err := db.Where("note_id = ? AND template = ?", note.ID, "t-card").First(&visible).Error; err != nil {
		t.Fatalf("load the visible card: %v", err)
	}
	var restoredWouldBe Card
	if err := db.Where("note_id = ? AND template = ?", note.ID, "t-second").First(&restoredWouldBe).Error; err != nil {
		t.Fatalf("load the second declared card: %v", err)
	}
	if err := db.Create(&CardState{CardID: visible.ID, UserID: owner, State: "review",
		DueAt: Ptr(time.Now().UTC()), Reps: 1, ScheduledDays: 1}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	if err := db.Create(&Review{CardID: visible.ID, UserID: owner, Rating: 3, GradeSource: "self",
		ReviewedAt: time.Now().UTC(), ReviewDay: time.Now().UTC().Format("2006-01-02")}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Model(&Card{}).Where("id = ?", restoredWouldBe.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("soft-delete the second card: %v", err)
	}
	return &lightPathFixture{ctx: ctx, db: db, nt: nt, noteID: note.ID,
		visibleCardID: visible.ID, softDeletedCardID: restoredWouldBe.ID}
}

// config returns the stored config so every light update keeps the second template declared and
// therefore keeps the Cards plan false.
func (f *lightPathFixture) config(t *testing.T) notetype.Config {
	t.Helper()
	cfg, err := f.nt.Config()
	if err != nil {
		t.Fatalf("decode note type config: %v", err)
	}
	return cfg
}

type lightPathSnapshot struct {
	noteType       NoteType
	cards          []Card
	cardStates     []CardState
	reviews        []Review
	mediaNotes     []MediaNote
	mediaNoteTypes []MediaNoteType
}

// loadLightPathSnapshot reads every row a note type save can touch for this fixture. Comparing two
// snapshots proves a call wrote nothing.
func loadLightPathSnapshot(t *testing.T, f *lightPathFixture) lightPathSnapshot {
	t.Helper()
	var out lightPathSnapshot
	if err := f.db.First(&out.noteType, f.nt.ID).Error; err != nil {
		t.Fatalf("load note type: %v", err)
	}
	if err := f.db.Unscoped().
		Where("note_id IN (SELECT id FROM notes WHERE note_type_id = ?)", f.nt.ID).
		Order("id ASC").Find(&out.cards).Error; err != nil {
		t.Fatalf("load cards: %v", err)
	}
	if err := f.db.
		Where("card_id IN (SELECT id FROM cards WHERE note_id IN (SELECT id FROM notes WHERE note_type_id = ?))", f.nt.ID).
		Order("card_id ASC").Find(&out.cardStates).Error; err != nil {
		t.Fatalf("load card states: %v", err)
	}
	if err := f.db.
		Where("card_id IN (SELECT id FROM cards WHERE note_id IN (SELECT id FROM notes WHERE note_type_id = ?))", f.nt.ID).
		Order("id ASC").Find(&out.reviews).Error; err != nil {
		t.Fatalf("load reviews: %v", err)
	}
	if err := f.db.
		Where("note_id IN (SELECT id FROM notes WHERE note_type_id = ?)", f.nt.ID).
		Order("media_sha ASC").Find(&out.mediaNotes).Error; err != nil {
		t.Fatalf("load media_notes: %v", err)
	}
	if err := f.db.Where("note_type_id = ?", f.nt.ID).Order("media_name ASC").Find(&out.mediaNoteTypes).Error; err != nil {
		t.Fatalf("load media_note_types: %v", err)
	}
	return out
}

// TestNoteTypeUpdateLightPath covers a real Update, not only the classifier:
//   - a light dry run reports zero impact and leaves the row, versions, both media mappings, cards,
//     card_states and reviews exactly as they were;
//   - a stale version is rejected and writes nothing;
//   - a CSS media reference replaces media_note_types but leaves media_notes, cards and progress alone.
//
// Every case keeps a soft-deleted card in place and asserts it stays soft-deleted: a presentation-only
// save is not a repair pass, which is the boundary of this optimization.
func TestNoteTypeUpdateLightPath(t *testing.T) {
	t.Run("dry run writes nothing", func(t *testing.T) {
		f := newLightPathFixture(t, `.card { background: url(_initial.png); }`)
		before := loadLightPathSnapshot(t, f)
		if len(before.mediaNoteTypes) != 1 || before.mediaNoteTypes[0].MediaName != "_initial.png" {
			t.Fatalf("initial media_note_types = %+v, want [_initial.png]", before.mediaNoteTypes)
		}

		update := f.nt
		update.Name = "Renamed"
		cfg := f.config(t)
		cfg.CSS = ".card { color: red; }"
		cfg.LaTeXPre = "\\documentclass{article}"
		cfg.SortFieldID = "f-back"
		capture := newSQLTraceLogger()
		types := NewNoteTypeStore(f.db.Session(&gorm.Session{Logger: capture}))
		capture.reset()
		impact, err := types.Update(f.ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0, DryRun: true})
		if err != nil {
			t.Fatalf("dry-run Update() error = %v", err)
		}
		if impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
			t.Errorf("dry-run impact = added %d removed %d, want 0/0", impact.CardsAdded, impact.CardsRemoved)
		}
		after := loadLightPathSnapshot(t, f)
		if !reflect.DeepEqual(before, after) {
			t.Errorf("a light dry run changed stored state:\nbefore %+v\nafter  %+v", before, after)
		}
		assertLightUpdateStatements(t, 1, "dry run", capture.snapshot())
	})

	t.Run("stale version writes nothing", func(t *testing.T) {
		f := newLightPathFixture(t, ".card { color: black; }")
		types := NewNoteTypeStore(f.db)
		light := f.config(t)
		light.CSS = ".card { color: #111; }"
		update := f.nt
		if _, err := types.Update(f.ctx, &update, light, NoteTypeUpdateOptions{ExpectedVersion: 0}); err != nil {
			t.Fatalf("initial light Update() error = %v", err)
		}
		before := loadLightPathSnapshot(t, f)
		if before.noteType.Version != 1 {
			t.Fatalf("version after the first light update = %d, want 1", before.noteType.Version)
		}

		current, err := types.ByID(f.ctx, f.nt.ID)
		if err != nil {
			t.Fatalf("load note type: %v", err)
		}
		stale := *current
		stale.Name = "Should not land"
		cfg := f.config(t)
		cfg.CSS = ".card { color: #222; }"
		if _, err := types.Update(f.ctx, &stale, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0}); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("stale Update() error = %v, want ErrVersionConflict", err)
		}
		after := loadLightPathSnapshot(t, f)
		if !reflect.DeepEqual(before, after) {
			t.Errorf("a rejected light update changed stored state:\nbefore %+v\nafter  %+v", before, after)
		}
	})

	t.Run("css media reference updates note type media only", func(t *testing.T) {
		f := newLightPathFixture(t, ".card { color: black; }")
		before := loadLightPathSnapshot(t, f)
		if len(before.mediaNoteTypes) != 0 {
			t.Fatalf("initial media_note_types = %+v, want none", before.mediaNoteTypes)
		}
		if len(before.mediaNotes) != 1 {
			t.Fatalf("initial media_notes = %+v, want one row", before.mediaNotes)
		}

		update := f.nt
		cfg := f.config(t)
		cfg.CSS = `.card { background: url(_bg.png); }`
		capture := newSQLTraceLogger()
		types := NewNoteTypeStore(f.db.Session(&gorm.Session{Logger: capture}))
		capture.reset()
		impact, err := types.Update(f.ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0})
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
			t.Errorf("impact = added %d removed %d, want 0/0", impact.CardsAdded, impact.CardsRemoved)
		}

		names, err := NewMediaNoteTypeStore(f.db).List(f.ctx, f.nt.ID)
		if err != nil {
			t.Fatalf("list media_note_types: %v", err)
		}
		if !reflect.DeepEqual(names, []string{"_bg.png"}) {
			t.Errorf("media_note_types = %v, want [_bg.png]", names)
		}
		after := loadLightPathSnapshot(t, f)
		if after.noteType.Version != 1 {
			t.Errorf("version = %d, want 1", after.noteType.Version)
		}
		if !reflect.DeepEqual(before.cards, after.cards) {
			t.Errorf("a CSS-only update changed card rows: before %+v after %+v", before.cards, after.cards)
		}
		if !reflect.DeepEqual(before.cardStates, after.cardStates) {
			t.Errorf("a CSS-only update changed card_states: before %+v after %+v", before.cardStates, after.cardStates)
		}
		if !reflect.DeepEqual(before.reviews, after.reviews) {
			t.Errorf("a CSS-only update changed reviews: before %+v after %+v", before.reviews, after.reviews)
		}
		if !reflect.DeepEqual(before.mediaNotes, after.mediaNotes) {
			t.Errorf("a CSS-only update changed media_notes: before %+v after %+v", before.mediaNotes, after.mediaNotes)
		}
		statements := capture.snapshot()
		if got := countStatements(statements, func(s string) bool { return sqlTouchesTable(s, "media_notes") }); got != 0 {
			t.Errorf("a CSS-only update touched media_notes %d time(s), want 0", got)
		}
		if got := countStatements(statements, func(s string) bool { return sqlTouchesTable(s, "media_note_types") }); got == 0 {
			t.Error("a CSS media reference did not rewrite media_note_types")
		}
	})

	// The soft-deleted row is the boundary of this optimization: only a field or template change
	// (or a note write) restores it; a presentation-only save never does.
	t.Run("presentation-only save does not restore a soft-deleted card", func(t *testing.T) {
		f := newLightPathFixture(t, ".card { color: black; }")
		types := NewNoteTypeStore(f.db)
		cfg := f.config(t)
		cfg.CSS = ".card { color: #333; }"
		update := f.nt
		if _, err := types.Update(f.ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		var drifted Card
		if err := f.db.Unscoped().First(&drifted, f.softDeletedCardID).Error; err != nil {
			t.Fatalf("load the soft-deleted card: %v", err)
		}
		if !drifted.DeletedAt.Valid {
			t.Error("a presentation-only save restored a soft-deleted card; only a field or template change may do that")
		}
		var visible Card
		if err := f.db.First(&visible, f.visibleCardID).Error; err != nil {
			t.Fatalf("load the visible card: %v", err)
		}
	})
}

// TestNoteTypeUpdateTemplateChangeRestoresSameCardID is the regression that bounds the fast path:
// when a template change does resync cards, a soft-deleted row whose template is still declared is
// restored in place, so the card id (and the progress attached to it) survives.
func TestNoteTypeUpdateTemplateChangeRestoresSameCardID(t *testing.T) {
	f := newLightPathFixture(t, ".card { color: black; }")
	types := NewNoteTypeStore(f.db)
	cfg := f.config(t)
	cfg.Templates[0].QFormat = `<div class="wrapped">{{Front}}</div>`
	update := f.nt
	impact, err := types.Update(f.ctx, &update, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0, ConfirmCardRemoval: true})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if impact.CardsAdded != 1 || impact.CardsRemoved != 0 {
		t.Errorf("impact = added %d removed %d, want 1/0", impact.CardsAdded, impact.CardsRemoved)
	}
	var restored Card
	if err := f.db.First(&restored, f.softDeletedCardID).Error; err != nil {
		t.Fatalf("load the restored card %d: %v", f.softDeletedCardID, err)
	}
	if restored.ID != f.softDeletedCardID || restored.Template != "t-second" || restored.Ordinal != 1 {
		t.Errorf("restored card = id %d template %q ordinal %d, want id %d template t-second ordinal 1",
			restored.ID, restored.Template, restored.Ordinal, f.softDeletedCardID)
	}
}

// testSQLiteDB returns a fresh SQLite test database. These tests count statements, so they run on
// one database only: the batch size is the SQLite bind-parameter bound they are guarding. It opens
// SQLite directly instead of going through testDatabases, so a machine with TEST_PG_DSN set does
// not create an unused PostgreSQL schema for a test that never reads it.
func testSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// cloneNoteTypeConfig copies the slices a case mutates, so the comparison sees a real old and new
// config instead of two views of one backing array.
func cloneNoteTypeConfig(cfg notetype.Config) notetype.Config {
	out := cfg
	out.Fields = append([]notetype.Field(nil), cfg.Fields...)
	out.Templates = append([]notetype.Template(nil), cfg.Templates...)
	return out
}

// TestNoteTypeDerivedPlan covers the classification the update uses to decide which derived data
// to rebuild. A missing dependency loses data; an extra one only costs work, so the cases spell out
// each input the derivations actually read.
func TestNoteTypeDerivedPlan(t *testing.T) {
	base := notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "t-front", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
	metadata := notetype.Field{ID: "f-meta", Name: notetype.MetadataFieldName}
	withField := func(field notetype.Field) notetype.Config {
		cfg := base
		cfg.Fields = append(append([]notetype.Field(nil), base.Fields...), field)
		return cfg
	}

	cases := []struct {
		name                  string
		from                  func() notetype.Config
		mutate                func(cfg *notetype.Config)
		wantSearch, wantMedia bool
		wantCards             bool
	}{
		{
			name:   "identical config rebuilds nothing",
			mutate: func(*notetype.Config) {},
		},
		{
			name:   "css is presentation only",
			mutate: func(cfg *notetype.Config) { cfg.CSS = ".card { color: red; }" },
		},
		{
			name:   "latex settings are presentation only",
			mutate: func(cfg *notetype.Config) { cfg.LaTeXPre = "\\documentclass{article}" },
		},
		{
			name:   "sort field is presentation only",
			mutate: func(cfg *notetype.Config) { cfg.SortFieldID = "f-back" },
		},
		{
			name:      "template answer format needs the card sync",
			mutate:    func(cfg *notetype.Config) { cfg.Templates[0].AFormat = "{{Back}} (answer)" },
			wantCards: true,
		},
		{
			name:      "template name needs the card sync",
			mutate:    func(cfg *notetype.Config) { cfg.Templates[0].Name = "Renamed" },
			wantCards: true,
		},
		{
			name:      "front format can change the card set",
			mutate:    func(cfg *notetype.Config) { cfg.Templates[0].QFormat = "{{#Back}}{{Front}}{{/Back}}" },
			wantCards: true,
		},
		{
			name: "adding a template changes ordinals",
			mutate: func(cfg *notetype.Config) {
				cfg.Templates = append(cfg.Templates, notetype.Template{ID: "t-back", Name: "Card 2", QFormat: "{{Back}}"})
			},
			wantCards: true,
		},
		{
			name: "removing a template changes the card set",
			mutate: func(cfg *notetype.Config) {
				cfg.Templates = nil
			},
			wantCards: true,
		},
		{
			name: "reordering templates changes ordinals",
			mutate: func(cfg *notetype.Config) {
				cfg.Templates = append(cfg.Templates, notetype.Template{ID: "t-back", Name: "Card 2", QFormat: "{{Back}}"})
				cfg.Templates[0], cfg.Templates[1] = cfg.Templates[1], cfg.Templates[0]
			},
			wantCards: true,
		},
		{
			name: "renaming a field changes rendering and search",
			mutate: func(cfg *notetype.Config) {
				cfg.Fields[0].Name = "Face"
				cfg.Templates[0].QFormat = "{{Face}}"
			},
			wantSearch: true,
			wantCards:  true,
		},
		{
			name:       "toggling a search flag rewrites search text and resyncs cards conservatively",
			mutate:     func(cfg *notetype.Config) { cfg.Fields[0].ExcludeFromSearch = true },
			wantSearch: true,
			wantCards:  true,
		},
		{
			name:      "field styling still resyncs cards conservatively",
			mutate:    func(cfg *notetype.Config) { cfg.Fields[0].Font = "Arial" },
			wantCards: true,
		},
		{
			name: "adding an ordinary field needs the card sync",
			mutate: func(cfg *notetype.Config) {
				cfg.Fields = append(cfg.Fields, notetype.Field{ID: "f-extra", Name: "Extra"})
			},
			wantSearch: true,
			wantCards:  true,
		},
		{
			name: "adding a metadata field changes the media mapping",
			mutate: func(cfg *notetype.Config) {
				cfg.Fields = append(cfg.Fields, metadata)
			},
			wantSearch: true,
			wantMedia:  true,
			wantCards:  true,
		},
		{
			name: "renaming an ordinary field into metadata revokes its media mapping",
			mutate: func(cfg *notetype.Config) {
				cfg.Fields[1].Name = notetype.MetadataFieldName
			},
			wantSearch: true,
			wantMedia:  true,
			wantCards:  true,
		},
		{
			name: "renaming a metadata field back to ordinary restores its media mapping",
			from: func() notetype.Config {
				cfg := base
				cfg.Fields = append(append([]notetype.Field(nil), base.Fields...), metadata)
				return cfg
			},
			mutate: func(cfg *notetype.Config) {
				cfg.Fields[2].Name = "Ordinary"
			},
			wantSearch: true,
			wantMedia:  true,
			wantCards:  true,
		},
		{
			name: "changing the kind changes the card rule",
			mutate: func(cfg *notetype.Config) {
				cfg.Kind = notetype.KindCloze
				cfg.Templates = []notetype.Template{{ID: "t-cloze", Name: "Cloze", QFormat: "{{cloze:Front}}"}}
			},
			wantCards: true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			oldCfg := base
			if test.from != nil {
				oldCfg = test.from()
			}
			updated := cloneNoteTypeConfig(oldCfg)
			test.mutate(&updated)
			plan := planNoteTypeDerived(oldCfg, updated)
			if plan.SearchText != test.wantSearch {
				t.Errorf("SearchText = %v, want %v", plan.SearchText, test.wantSearch)
			}
			if plan.MediaNotes != test.wantMedia {
				t.Errorf("MediaNotes = %v, want %v", plan.MediaNotes, test.wantMedia)
			}
			if plan.Cards != test.wantCards {
				t.Errorf("Cards = %v, want %v", plan.Cards, test.wantCards)
			}
			if plan.loadsNotes() != (test.wantSearch || test.wantMedia || test.wantCards) {
				t.Errorf("loadsNotes() = %v with plan %+v", plan.loadsNotes(), plan)
			}
		})
	}

	// A metadata field that keeps its id but whose neighbour changes does not move the media
	// mapping: mediaFieldValues excludes by the metadata field id, not by position.
	reordered := withField(metadata)
	reordered.Fields[0], reordered.Fields[2] = reordered.Fields[2], reordered.Fields[0]
	if plan := planNoteTypeDerived(withField(metadata), reordered); plan.MediaNotes {
		t.Errorf("reordering around an unchanged metadata field marked the media mapping stale: %+v", plan)
	}
}
