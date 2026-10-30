package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

// autoMigratePreNoteTypeNotNull builds the schema with notes.note_type_id nullable, the shape a
// database has before migration 0013. The current model declares the column NOT NULL, and the
// fixtures in this file must insert a note that has no type yet.
func autoMigratePreNoteTypeNotNull(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := autoMigrate(context.Background(), db, &preNoteTypeNotNullNote{}); err != nil {
		t.Fatalf("auto migrate pre-0013 notes shape: %v", err)
	}
}

// openMigrationDB opens a fresh SQLite database for one migration case.
func openMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// migrationDatabases returns the SQLite case always, and the PostgreSQL case when TEST_PG_DSN is set.
func migrationDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	return testDatabases(t)
}

// seededNote is one note created through the old write path, together with the cards it generated and the kind that
// identified it before the migration dropped the column.
type seededNote struct {
	Kind   string
	ID     uint64
	Cards  []Card
	Fields map[string]any
}

// seedOldNote writes one note the way the pre-migration model wrote it: notes.kind carries the kind, the field values
// are the old typed JSON, and there is no note type. The note row is inserted directly, because the current model no
// longer has a kind column.
func seedOldNote(t *testing.T, db *gorm.DB, deckID uint64, kind string, fields map[string]any) seededNote {
	t.Helper()
	requireLegacyKindColumn(t, db)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode old fields for kind %q: %v", kind, err)
	}
	now := time.Now().UTC()
	note := Note{DeckID: deckID, FieldsJSON: string(raw), TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	note.PublicID = NewPublicID()
	if err := db.Exec(
		`INSERT INTO notes (public_id, deck_id, kind, fields_json, search_text, tags_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		note.PublicID, deckID, kind, note.FieldsJSON, "", note.TagsJSON, now, now,
	).Error; err != nil {
		t.Fatalf("insert old note kind %q: %v", kind, err)
	}
	if err := db.Model(&Note{}).Where("public_id = ?", note.PublicID).Pluck("id", &note.ID).Error; err != nil || note.ID == 0 {
		t.Fatalf("read back old note kind %q: %v", kind, err)
	}
	cards := make([]Card, 0, 2)
	for ordinal, template := range legacyCardTemplates(kind, fields) {
		card := Card{NoteID: note.ID, Template: template, Ordinal: ordinal, CreatedAt: now}
		if err := db.Create(&card).Error; err != nil {
			t.Fatalf("insert old card %q of kind %q: %v", template, kind, err)
		}
		cards = append(cards, card)
	}
	return seededNote{Kind: kind, ID: note.ID, Cards: cards, Fields: fields}
}

// requireLegacyKindColumn adds notes.kind on a database built by the current models: the migration under test reads
// that column, and AutoMigrate no longer creates it.
func requireLegacyKindColumn(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasColumn("notes", "kind") {
		return
	}
	if err := db.Exec(`ALTER TABLE notes ADD COLUMN kind TEXT NOT NULL DEFAULT ''`).Error; err != nil {
		t.Fatalf("add legacy notes.kind column: %v", err)
	}
}

// legacyCardTemplates returns the card templates the old model generated for a kind, in ordinal order.
func legacyCardTemplates(kind string, fields map[string]any) []string {
	switch kind {
	case "basic_both":
		return []string{"forward", "reverse"}
	case "cloze":
		text, _ := fields["text"].(string)
		var templates []string
		for number := 1; number <= 499; number++ {
			marker := "{{c" + strconv.Itoa(number) + "::"
			if strings.Contains(text, marker) {
				templates = append(templates, "cloze:"+strconv.Itoa(number))
			}
		}
		return templates
	default:
		return []string{"forward"}
	}
}

// assertBlocksMigration runs Sync and expects migration 0012 to refuse because of a blocking note.
func assertBlocksMigration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if _, err := Sync(context.Background(), db, BuiltinMigrations); err == nil {
		t.Fatal("Sync() error = nil, want the migration to refuse a blocking note")
	} else if !strings.Contains(err.Error(), "block") {
		t.Fatalf("Sync() error = %v, want a blocking-note error", err)
	}
}

// TestCheckNotesMigrationReportsBlockersAndWarnings covers the read-only pre-flight: a newline inside an array item
// blocks, a cloze pipe only warns, and the report names the deck, the note and the field.
func TestCheckNotesMigrationReportsBlockersAndWarnings(t *testing.T) {
	for driver, db := range migrationDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			autoMigratePreNoteTypeNotNull(t, db)
			ctx := context.Background()
			owner := seedUsers(t, db, "migration_preflight_owner")[0]
			deckID := seedDeck(t, db, owner)

			blocked := seedOldNote(t, db, deckID, "list", map[string]any{
				"prompt": "Name them", "items": []any{"one\ntwo", "three"},
			})
			warned := seedOldNote(t, db, deckID, "cloze", map[string]any{
				"text": "{{c1::Paris|City}} is a city. \\alt still ordinary.",
			})

			report, err := CheckNotesMigration(ctx, db)
			if err != nil {
				t.Fatalf("CheckNotesMigration() error = %v", err)
			}
			if !report.Blocked() {
				t.Fatal("report.Blocked() = false, want true")
			}
			if len(report.Blocking) != 1 {
				t.Fatalf("blocking issues = %d, want 1: %+v", len(report.Blocking), report.Blocking)
			}
			if got := report.Blocking[0]; got.Field != "items" || !strings.Contains(got.Reason, "newline") {
				t.Errorf("blocking issue = %+v, want the items field with a newline reason", got)
			}
			if len(report.Warnings) != 2 {
				t.Fatalf("warnings = %d, want 2 (pipe and \\alt): %+v", len(report.Warnings), report.Warnings)
			}
			for _, warning := range report.Warnings {
				if warning.Field != "text" {
					t.Errorf("warning field = %q, want text", warning.Field)
				}
			}
			// The public ids are what an operator can search for in the application.
			var deckPublic, notePublic string
			if err := db.Table("decks").Where("id = ?", deckID).Pluck("public_id", &deckPublic).Error; err != nil {
				t.Fatalf("read deck public id: %v", err)
			}
			if err := db.Unscoped().Table("notes").Where("id = ?", blocked.ID).Pluck("public_id", &notePublic).Error; err != nil {
				t.Fatalf("read note public id: %v", err)
			}
			if report.Blocking[0].DeckID != deckPublic || report.Blocking[0].NoteID != notePublic {
				t.Errorf("issue ids = %s/%s, want deck %s note %s", report.Blocking[0].DeckID, report.Blocking[0].NoteID, deckPublic, notePublic)
			}
			// A warning alone does not block: the conversion converts the cloze text as ordinary content.
			if warned.ID == 0 {
				t.Fatal("cloze note was not created")
			}
			assertBlocksMigration(t, db)
		})
	}
}

// legacyDeckWithoutPublicID is the decks table shape before decks carried a
// public id. The pre-flight check must read that schema, so the test builds it
// directly instead of running AutoMigrate over the current model, which would
// add the missing column and hide the bug.
type legacyDeckWithoutPublicID struct {
	ID          uint64 `gorm:"primaryKey"`
	OwnerUserID uint64 `gorm:"column:owner_user_id"`
	Name        string `gorm:"column:name"`
}

func (legacyDeckWithoutPublicID) TableName() string { return "decks" }

// legacyNoteWithKind is the notes table shape the pre-flight check reads: the
// old kind column plus the fields the report needs, without the rest of the
// current model.
type legacyNoteWithKind struct {
	ID         uint64 `gorm:"primaryKey"`
	PublicID   string `gorm:"column:public_id"`
	DeckID     uint64 `gorm:"column:deck_id"`
	Kind       string `gorm:"column:kind"`
	FieldsJSON string `gorm:"column:fields_json"`
}

func (legacyNoteWithKind) TableName() string { return "notes" }

// TestCheckNotesMigrationWithoutDeckPublicID covers a database older than the
// deck public id: the check must report the note issues by numeric deck id
// instead of failing on the missing column.
func TestCheckNotesMigrationWithoutDeckPublicID(t *testing.T) {
	for driver, db := range migrationDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(&legacyDeckWithoutPublicID{}, &legacyNoteWithKind{}); err != nil {
				t.Fatalf("auto migrate pre-public-id schema: %v", err)
			}
			if db.Migrator().HasColumn("decks", "public_id") {
				t.Fatal("decks.public_id exists, want the schema before it was added")
			}
			deck := legacyDeckWithoutPublicID{OwnerUserID: 1, Name: "Legacy deck"}
			if err := db.Create(&deck).Error; err != nil {
				t.Fatalf("create legacy deck: %v", err)
			}
			note := legacyNoteWithKind{
				PublicID: NewPublicID(), DeckID: deck.ID, Kind: "list",
				FieldsJSON: `{"prompt":"Name them","items":["one\ntwo"]}`,
			}
			if err := db.Create(&note).Error; err != nil {
				t.Fatalf("create legacy note: %v", err)
			}

			report, err := CheckNotesMigration(context.Background(), db)
			if err != nil {
				t.Fatalf("CheckNotesMigration() error = %v, want a report", err)
			}
			if len(report.Blocking) != 1 {
				t.Fatalf("blocking issues = %d, want 1: %+v", len(report.Blocking), report.Blocking)
			}
			issue := report.Blocking[0]
			if want := strconv.FormatUint(deck.ID, 10); issue.DeckID != want {
				t.Errorf("issue deck id = %q, want the numeric id %q", issue.DeckID, want)
			}
			if issue.NoteID != note.PublicID {
				t.Errorf("issue note id = %q, want %q", issue.NoteID, note.PublicID)
			}
		})
	}
}

// TestMigrationConvertsNotesToNoteTypes is the main migration case: one note of every kind, including audio, TTS,
// formulas and media, is converted; note types are created per deck and kind; cards, card states and reviews are
// untouched.
func TestMigrationConvertsNotesToNoteTypes(t *testing.T) {
	for driver, db := range migrationDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			autoMigratePreNoteTypeNotNull(t, db)
			ctx := context.Background()
			owner := seedUsers(t, db, "migration_owner")[0]
			deckID := seedDeck(t, db, owner)
			now := time.Now().UTC()

			imgSha := strings.Repeat("a", 64)
			audSha := strings.Repeat("b", 64)
			for _, media := range []Media{
				{Sha256: imgSha, RelPath: "aa/" + imgSha + ".png", Mime: "image/png", Bytes: 10, CreatedAt: now},
				{Sha256: audSha, RelPath: "bb/" + audSha + ".mp3", Mime: "audio/mpeg", Bytes: 20, CreatedAt: now},
			} {
				row := media
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("create media: %v", err)
				}
			}

			notes := []seededNote{
				seedOldNote(t, db, deckID, "basic", map[string]any{
					"front":      "Listen {{tts:bonjour|lang=fr-FR|rate=0.9}} and {{tts:secret|hide}} then {{audio:/media/" + audSha + "|start=1|end=2|rate=1.5|loop}} ![diagram](/media/" + imgSha + ")",
					"back":       "Plain audio {{audio:/media/" + audSha + "}} and a formula \\(x^2\\).",
					"source_url": "https://example.com/source",
					"extra":      map[string]any{"note": "extra content"},
				}),
				seedOldNote(t, db, deckID, "basic_both", map[string]any{
					"front": "der Hund\nlaeuft", "back": "the dog\nruns",
				}),
				seedOldNote(t, db, deckID, "cloze", map[string]any{
					"text": "{{c1::Paris|City::city}} is the capital of {{c2::France}} and $$x^2$$ stays.",
				}),
				seedOldNote(t, db, deckID, "typed", map[string]any{
					"prompt": "Which element has the symbol Au?\nGold is \\(Au\\).",
					"answer": "gold", "accept": []any{"aurum"},
					"ignore_case": true, "ignore_whitespace": true,
					"regex": "^a*b_c&d$",
				}),
				seedOldNote(t, db, deckID, "numeric", map[string]any{
					"prompt": "What is \\(g_0\\)?", "value": 9.80665, "unit": "m/s²",
					"tolerance_relative": 0.01,
				}),
				seedOldNote(t, db, deckID, "choice_single", map[string]any{
					"question": "Which planet is closest to the Sun?",
					"options":  []any{"Venus", "Mercury", "Mars"}, "answer": 1,
				}),
				seedOldNote(t, db, deckID, "choice_multi", map[string]any{
					"question": "Which are prime?", "options": []any{"2", "4", "5"},
					"answers": []any{0, 2},
				}),
				seedOldNote(t, db, deckID, "true_false", map[string]any{
					"statement": "Water boils at 100 °C.", "answer": true,
				}),
				seedOldNote(t, db, deckID, "list", map[string]any{
					"prompt": "Name a primary colour.", "items": []any{"red", "green"},
					"ordered": false,
				}),
				seedOldNote(t, db, deckID, "short_answer", map[string]any{
					"prompt": "Why does the Moon show phases?", "reference": "The Sun lights half of it.",
				}),
			}

			// Snapshot content that must not change.
			type cardRow struct {
				ID       uint64
				NoteID   uint64
				Template string
				Ordinal  int
			}
			var beforeCards []cardRow
			if err := db.Model(&Card{}).Order("id ASC").Find(&beforeCards).Error; err != nil {
				t.Fatalf("snapshot cards: %v", err)
			}
			for i, note := range notes {
				if len(note.Cards) == 0 {
					t.Fatalf("note kind %q generated no cards", note.Kind)
				}
				card := note.Cards[0]
				state := CardState{CardID: card.ID, UserID: owner, State: "review", Reps: i + 1, Lapses: i}
				if err := db.Create(&state).Error; err != nil {
					t.Fatalf("seed card state: %v", err)
				}
				review := Review{
					CardID: card.ID, UserID: owner, Rating: 3, GradeSource: "self",
					ReviewedAt: now, ReviewDay: "2026-10-30", StateBefore: 2,
				}
				if err := db.Create(&review).Error; err != nil {
					t.Fatalf("seed review: %v", err)
				}
			}
			var beforeStates, beforeReviews int64
			db.Model(&CardState{}).Count(&beforeStates)
			db.Model(&Review{}).Count(&beforeReviews)

			if _, err := Sync(ctx, db, BuiltinMigrations); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			// The final schema shape: kind is gone and note_type_id exists. The column's NOT NULL
			// constraint is read from the database catalog by TestSyncKeepsNoteTypeIDNotNull.
			if db.Migrator().HasColumn("notes", "kind") {
				t.Error("notes.kind still exists after migration 0013")
			}
			if !db.Migrator().HasColumn("notes", "note_type_id") {
				t.Fatal("notes.note_type_id is missing")
			}

			// The conversion is one note type per (deck, kind).
			var types []NoteType
			if err := db.Where("deck_id = ?", deckID).Order("name ASC").Find(&types).Error; err != nil {
				t.Fatalf("read note types: %v", err)
			}
			if len(types) != len(notes) {
				t.Fatalf("note types = %d, want %d", len(types), len(notes))
			}
			byName := map[string]NoteType{}
			for _, nt := range types {
				byName[nt.Name] = nt
			}

			// Every note now points at the type of its kind and holds converted HTML.
			type noteRow struct {
				ID         uint64
				NoteTypeID uint64
				FieldsJSON string `gorm:"column:fields_json"`
				SourceURL  *string
				ExtraJSON  *string
				SearchText string
			}
			var after []noteRow
			if err := db.Table("notes").
				Select("id, note_type_id, fields_json, source_url, extra_json, search_text").
				Order("id ASC").Scan(&after).Error; err != nil {
				t.Fatalf("read converted notes: %v", err)
			}
			fieldsByKind := map[string]map[string]any{}
			noteIDByKind := map[string]uint64{}
			for _, note := range notes {
				noteIDByKind[note.Kind] = note.ID
			}
			for _, row := range after {
				kind := ""
				for _, note := range notes {
					if note.ID == row.ID {
						kind = note.Kind
					}
				}
				if kind == "" {
					continue
				}
				if row.NoteTypeID == 0 {
					t.Errorf("note kind %q has no note type", kind)
				}
				fields, err := ParseFields(row.FieldsJSON)
				if err != nil {
					t.Fatalf("note kind %q fields: %v", kind, err)
				}
				fieldsByKind[kind] = fields
				stock, _ := notetype.EngramStockByKind(kind)
				if nt := byName[stock.Name]; nt.ID != row.NoteTypeID {
					t.Errorf("note kind %q note_type_id = %d, want the %q type %d", kind, row.NoteTypeID, stock.Name, nt.ID)
				}
				if row.SearchText == "" {
					t.Errorf("note kind %q has empty search_text", kind)
				}
			}

			// Field values: Markdown to HTML, formulas kept, every shortcode in Anki syntax, media renamed.
			basicFront := htmlField(t, fieldsByKind["basic"], "front")
			if !strings.Contains(basicFront, "Listen bonjour[anki:tts lang=fr_FR speed=0.9]bonjour[/anki:tts]") {
				t.Errorf("basic front TTS = %q", basicFront)
			}
			if !strings.Contains(basicFront, "[anki:tts]secret[/anki:tts]") ||
				strings.Contains(basicFront, "secret[anki:tts]secret[/anki:tts]") {
				t.Errorf("basic front hidden TTS = %q, want the leading visible copy omitted", basicFront)
			}
			if !strings.Contains(basicFront, `<span class="engram-sound" data-start="1" data-end="2" data-rate="1.5" data-loop="1">[sound:`+audSha+`.mp3]</span>`) {
				t.Errorf("basic front audio = %q", basicFront)
			}
			if !strings.Contains(basicFront, `<img src="`+imgSha+`.png" alt="diagram">`) {
				t.Errorf("basic front media = %q", basicFront)
			}
			basicBack := htmlField(t, fieldsByKind["basic"], "back")
			if !strings.Contains(basicBack, "[sound:"+audSha+".mp3]") {
				t.Errorf("basic back plain audio = %q", basicBack)
			}
			if !strings.Contains(basicBack, `\(x^2\)`) {
				t.Errorf("basic back formula = %q", basicBack)
			}
			if _, ok := fieldsByKind["basic"]["source_url"]; ok {
				t.Error("source_url is still a field; it must move to the column")
			}
			if _, ok := fieldsByKind["basic"]["extra"]; ok {
				t.Error("extra is still a field; it must move to the column")
			}
			for _, row := range after {
				if row.ID != noteIDByKind["basic"] {
					continue
				}
				if row.SourceURL == nil || *row.SourceURL != "https://example.com/source" {
					t.Errorf("source_url column = %v", row.SourceURL)
				}
				if row.ExtraJSON == nil || !strings.Contains(*row.ExtraJSON, "extra content") {
					t.Errorf("extra_json column = %v", row.ExtraJSON)
				}
			}

			bBoth := fieldsByKind["basic_both"]
			if got := htmlField(t, bBoth, "front"); !strings.Contains(got, "der Hund<br>") {
				t.Errorf("basic_both front hard wrap = %q", got)
			}
			if got := htmlField(t, bBoth, "front"); got != "<p>der Hund<br>\nlaeuft</p>\n" {
				t.Errorf("basic_both front = %q", got)
			}

			cloze := htmlField(t, fieldsByKind["cloze"], "text")
			if !strings.Contains(cloze, "{{c1::Paris|City::city}}") {
				t.Errorf("cloze text = %q", cloze)
			}
			if !strings.Contains(cloze, "$$x^2$$") {
				t.Errorf("cloze dollar formula = %q", cloze)
			}

			typed := fieldsByKind["typed"]
			if got := htmlField(t, typed, "prompt"); !strings.Contains(got, `\(Au\)`) {
				t.Errorf("typed prompt formula = %q", got)
			}
			if got := htmlField(t, typed, "accept"); got != "aurum" {
				t.Errorf("typed accept = %q, want the array joined as one line", got)
			}
			if got := htmlField(t, typed, "ignore_case"); got != "true" {
				t.Errorf("typed ignore_case = %q", got)
			}
			// The typed kind's regex field is real in the old grading path even
			// though the old editor table omitted it; it must survive and stay
			// bound to the text control's regex role.
			if got := htmlField(t, typed, "regex"); got != "^a*b_c&amp;d$" {
				t.Errorf("typed regex = %q, want the pattern HTML-escaped as plain text", got)
			}
			if got := notetype.PlainText(htmlField(t, typed, "regex")); got != "^a*b_c&d$" {
				t.Errorf("typed regex PlainText = %q, want the original pattern", got)
			}
			typedType, ok := byName["Typed answer"]
			if !ok {
				t.Fatal("the typed note type was not created")
			}
			typedCfg, err := typedType.Config()
			if err != nil {
				t.Fatalf("typed config: %v", err)
			}
			if !configHasField(typedCfg, "regex") {
				t.Error("the typed note type has no regex field")
			}
			if !configFieldPlainText(typedCfg, "regex") {
				t.Error("the typed regex field is not plain_text")
			}
			if len(typedCfg.Templates) != 1 || typedCfg.Templates[0].Answer == nil {
				t.Fatalf("typed templates = %+v, want one template with an answer spec", typedCfg.Templates)
			}
			if got := typedCfg.Templates[0].Answer.Bindings["regex"]; got != "regex" {
				t.Errorf("typed regex binding = %q, want %q", got, "regex")
			}

			numeric := fieldsByKind["numeric"]
			if got := htmlField(t, numeric, "value"); got != "9.80665" {
				t.Errorf("numeric value = %q", got)
			}
			if got := htmlField(t, numeric, "tolerance_relative"); got != "0.01" {
				t.Errorf("numeric tolerance_relative = %q", got)
			}

			choiceSingle := fieldsByKind["choice_single"]
			if got := htmlField(t, choiceSingle, "options"); got != "Venus<br>Mercury<br>Mars" {
				t.Errorf("choice_single options = %q", got)
			}
			if got := htmlField(t, choiceSingle, "answer"); got != "1" {
				t.Errorf("choice_single answer = %q", got)
			}

			trueFalse := fieldsByKind["true_false"]
			if got := htmlField(t, trueFalse, "answer"); got != "true" {
				t.Errorf("true_false answer = %q", got)
			}

			list := fieldsByKind["list"]
			if got := htmlField(t, list, "items"); got != "red<br>green" {
				t.Errorf("list items = %q", got)
			}
			if got := htmlField(t, list, "ordered"); got != "false" {
				t.Errorf("list ordered = %q", got)
			}

			// Note type shapes: ids and names keep today's field names; templates are the table's.
			wantTemplates := map[string][]string{
				"Basic":                   {"forward"},
				"Basic (both directions)": {"forward", "reverse"},
				"Cloze":                   {"cloze"},
				"Typed answer":            {"forward"},
				"Numeric answer":          {"forward"},
				"Single choice":           {"forward"},
				"Multiple choice":         {"forward"},
				"True / false":            {"forward"},
				"List":                    {"forward"},
				"Short answer":            {"forward"},
			}
			for name, want := range wantTemplates {
				nt, ok := byName[name]
				if !ok {
					t.Errorf("note type %q was not created", name)
					continue
				}
				cfg, err := nt.Config()
				if err != nil {
					t.Errorf("note type %q config: %v", name, err)
					continue
				}
				var got []string
				for _, template := range cfg.Templates {
					got = append(got, template.ID)
				}
				if strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("note type %q templates = %v, want %v", name, got, want)
				}
				for _, field := range cfg.Fields {
					if field.ID != field.Name {
						t.Errorf("note type %q field id %q != name %q; today's field names are the ids", name, field.ID, field.Name)
					}
				}
				if cfg.SortFieldID != cfg.Fields[0].ID {
					t.Errorf("note type %q sort field = %q, want the first field %q", name, cfg.SortFieldID, cfg.Fields[0].ID)
				}
				if cfg.OriginalStockKind != nil {
					t.Errorf("note type %q original_stock_kind = %v, want null (an engram kind, not an Anki stock kind)", name, *cfg.OriginalStockKind)
				}
			}
			// The cloze type is a cloze note type with one template and the cloze control.
			clozeType := byName["Cloze"]
			clozeCfg, err := clozeType.Config()
			if err != nil {
				t.Fatalf("cloze config: %v", err)
			}
			if clozeCfg.Kind != notetype.KindCloze {
				t.Errorf("cloze kind = %q, want cloze", clozeCfg.Kind)
			}
			if len(clozeCfg.Templates) != 1 || clozeCfg.Templates[0].Answer == nil ||
				clozeCfg.Templates[0].Answer.Control != "cloze_blanks" {
				t.Errorf("cloze template = %+v, want one cloze_blanks template", clozeCfg.Templates)
			}

			// cards.template values are unchanged.
			type cardAfter struct {
				ID       uint64
				NoteID   uint64
				Template string
				Ordinal  int
			}
			var afterCards []cardAfter
			if err := db.Model(&Card{}).Order("id ASC").Find(&afterCards).Error; err != nil {
				t.Fatalf("read cards after migration: %v", err)
			}
			if len(afterCards) != len(beforeCards) {
				t.Fatalf("cards = %d, want %d unchanged", len(afterCards), len(beforeCards))
			}
			for i := range beforeCards {
				if beforeCards[i].ID != afterCards[i].ID || beforeCards[i].Template != afterCards[i].Template ||
					beforeCards[i].Ordinal != afterCards[i].Ordinal {
					t.Errorf("card %d changed: before %+v after %+v", i, beforeCards[i], afterCards[i])
				}
			}
			var afterStates, afterReviews int64
			db.Model(&CardState{}).Count(&afterStates)
			db.Model(&Review{}).Count(&afterReviews)
			if afterStates != beforeStates || afterReviews != beforeReviews {
				t.Errorf("progress counts = %d states %d reviews, want %d and %d", afterStates, afterReviews, beforeStates, beforeReviews)
			}

			// Media names: one deck-scoped name per referenced file, with the extension from the mime table.
			var names []MediaName
			if err := db.Where("deck_id = ?", deckID).Order("name ASC").Find(&names).Error; err != nil {
				t.Fatalf("read media names: %v", err)
			}
			wantNames := map[string]string{imgSha + ".png": imgSha, audSha + ".mp3": audSha}
			if len(names) != len(wantNames) {
				t.Fatalf("media names = %+v, want %d rows", names, len(wantNames))
			}
			for _, row := range names {
				if wantNames[row.Name] != row.Sha256 {
					t.Errorf("media name %q -> %q, want %q", row.Name, row.Sha256, wantNames[row.Name])
				}
			}
		})
	}
}

// TestMigrationPreservesCardPlainText pins the plain text of every seeded card after the conversion.
//
// The expectations are the old engine's output at the switch-over, frozen here: the old card type package is gone, so
// the table is the only surviving reference for what the migrated content must still read as. The fixture has no audio,
// TTS or media: those forms change the visible text by design (a sound tag is not shown, a TTS directive is spoken), and
// their conversion is asserted exactly by the main case.
func TestMigrationPreservesCardPlainText(t *testing.T) {
	for driver, db := range migrationDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			autoMigratePreNoteTypeNotNull(t, db)
			ctx := context.Background()
			owner := seedUsers(t, db, "migration_text_owner")[0]
			deckID := seedDeck(t, db, owner)

			notes := []seededNote{
				seedOldNote(t, db, deckID, "basic", map[string]any{
					"front": "What is **bold**?", "back": "An answer with \\(x^2\\).",
				}),
				seedOldNote(t, db, deckID, "basic_both", map[string]any{
					"front": "der Hund", "back": "the dog",
				}),
				seedOldNote(t, db, deckID, "cloze", map[string]any{
					"text": "{{c1::Paris|City::hint}} is in France.",
				}),
				seedOldNote(t, db, deckID, "typed", map[string]any{
					"prompt": "Which element is Au?", "answer": "gold", "accept": []any{"aurum"},
				}),
				seedOldNote(t, db, deckID, "numeric", map[string]any{
					"prompt": "Standard gravity?", "value": 9.80665, "unit": "m/s²",
				}),
				seedOldNote(t, db, deckID, "choice_single", map[string]any{
					"question": "Closest planet?", "options": []any{"Venus", "Mercury"}, "answer": 1,
				}),
				seedOldNote(t, db, deckID, "choice_multi", map[string]any{
					"question": "Which are prime?", "options": []any{"2", "4", "5"}, "answers": []any{0, 2},
				}),
				seedOldNote(t, db, deckID, "true_false", map[string]any{
					"statement": "Water boils at 100 °C.", "answer": false,
				}),
				seedOldNote(t, db, deckID, "list", map[string]any{
					"prompt": "Name a primary colour.", "items": []any{"red"}, "ordered": false,
				}),
				seedOldNote(t, db, deckID, "short_answer", map[string]any{
					"prompt": "Why phases?", "reference": "The Sun lights half of the Moon.",
				}),
			}

			if _, err := Sync(ctx, db, BuiltinMigrations); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			type noteRow struct {
				ID         uint64
				FieldsJSON string `gorm:"column:fields_json"`
			}
			var rows []noteRow
			if err := db.Table("notes").Select("id, fields_json").Order("id ASC").Scan(&rows).Error; err != nil {
				t.Fatalf("read converted notes: %v", err)
			}
			converted := map[string]map[string]any{}
			for _, note := range notes {
				for _, row := range rows {
					if row.ID != note.ID {
						continue
					}
					fields, err := ParseFields(row.FieldsJSON)
					if err != nil {
						t.Fatalf("converted note kind %q: %v", note.Kind, err)
					}
					converted[note.Kind] = fields
				}
			}

			for _, note := range notes {
				stock, _ := notetype.EngramStockByKind(note.Kind)
				fields := stringFields(converted[note.Kind])
				for _, card := range note.Cards {
					templateID := card.Template
					ordinal := clozeOrdinal(card.Template)
					if stock.Config.Kind == notetype.KindCloze {
						templateID = "cloze"
					}
					template, ok := stockTemplate(stock.Config, templateID)
					if !ok {
						t.Fatalf("stock kind %q has no template %q", note.Kind, templateID)
					}
					for _, side := range []notetype.Side{notetype.SideFront, notetype.SideBack} {
						face, err := notetype.Render(stock.Config, template, side, notetype.RenderData{
							Fields: fields, ClozeOrdinal: ordinal,
						})
						if err != nil {
							t.Fatalf("new render kind %q %s: %v", note.Kind, side, err)
						}
						if side == notetype.SideBack && template.Answer != nil {
							face, err = renderAnswerControl(*template.Answer, face, fields, ordinal)
							if err != nil {
								t.Fatalf("new answer control kind %q back: %v", note.Kind, err)
							}
						}
						got := notetype.PlainText(face)
						want := plainTextExpectations[note.Kind+"/"+card.Template+"/"+side.String()]
						if got != want {
							t.Errorf("kind %q card %q %s plain text changed:\n want %q\n got  %q", note.Kind, card.Template, side, want, got)
						}
					}
				}
			}
		})
	}
}

// plainTextExpectations is the plain text of every card face the migration fixture produces, keyed by
// "<kind>/<template>/<side>". See the test comment for why the values are frozen here.
var plainTextExpectations = map[string]string{
	"basic/forward/front":         "What is bold?",
	"basic/forward/back":          `An answer with \(x^2\).`,
	"basic_both/forward/front":    "der Hund",
	"basic_both/forward/back":     "the dog",
	"basic_both/reverse/front":    "the dog",
	"basic_both/reverse/back":     "der Hund",
	"cloze/cloze:1/front":         "[hint] is in France.",
	"cloze/cloze:1/back":          "Paris|City is in France.",
	"typed/forward/front":         "Which element is Au?",
	"typed/forward/back":          "gold / aurum",
	"numeric/forward/front":       "Standard gravity?",
	"numeric/forward/back":        "9.80665 m/s²",
	"choice_single/forward/front": "Closest planet?",
	"choice_single/forward/back":  "Mercury",
	"choice_multi/forward/front":  "Which are prime?",
	"choice_multi/forward/back":   "2, 5",
	"true_false/forward/front":    "Water boils at 100 °C.",
	"true_false/forward/back":     "false",
	"list/forward/front":          "Name a primary colour.",
	"list/forward/back":           "red",
	"short_answer/forward/front":  "Why phases?",
	"short_answer/forward/back":   "The Sun lights half of the Moon.",
}

// configFieldPlainText reports whether the named field carries the plain_text flag.
func configFieldPlainText(cfg notetype.Config, id string) bool {
	for _, field := range cfg.Fields {
		if field.ID == id {
			return field.PlainText
		}
	}
	return false
}

// configHasField reports whether a note type config declares this field id.
func configHasField(cfg notetype.Config, id string) bool {
	for _, field := range cfg.Fields {
		if field.ID == id {
			return true
		}
	}
	return false
}

// htmlField reads one converted field as a string.
func htmlField(t *testing.T, fields map[string]any, key string) string {
	t.Helper()
	value, ok := fields[key]
	if !ok {
		t.Fatalf("field %q is missing", key)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("field %q = %T, want a string", key, value)
	}
	return text
}

// stringFields narrows a field map to the id -> HTML form RenderData needs.
func stringFields(fields map[string]any) map[string]string {
	out := make(map[string]string, len(fields))
	for key, value := range fields {
		if text, ok := value.(string); ok {
			out[key] = text
		}
	}
	return out
}

// clozeOrdinal returns the cloze number in a "cloze:<n>" card template, or 0 for a normal card.
func clozeOrdinal(template string) int {
	rest, ok := strings.CutPrefix(template, "cloze:")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return n
}

// stockTemplate finds one template by id.
func stockTemplate(cfg notetype.Config, id string) (notetype.Template, bool) {
	for _, template := range cfg.Templates {
		if template.ID == id {
			return template, true
		}
	}
	return notetype.Template{}, false
}

// typeMarkerRE finds the exact type placeholder the template renderer emitted.
var typeMarkerRE = regexp.MustCompile(`\[\[type:[^\]]+\]\]`)

// TestSyncKeepsNoteTypeIDNotNull covers the three states a start can find and reads the constraint
// from the database catalog, not from the model: a fresh database, a database that still holds
// notes without a type (below migration 0013), and a database at version 14 whose column was
// loosened to nullable by a later additive pass. After the first Sync and after a second one,
// notes.note_type_id is NOT NULL in all three, and the database itself rejects a note without a
// type.
func TestSyncKeepsNoteTypeIDNotNull(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, db *gorm.DB)
	}{
		{
			name:  "fresh",
			build: func(t *testing.T, db *gorm.DB) {},
		},
		{
			name: "pre_note_types",
			build: func(t *testing.T, db *gorm.DB) {
				autoMigratePreNoteTypeNotNull(t, db)
				owner := seedUsers(t, db, "note_type_not_null_legacy")[0]
				deckID := seedDeck(t, db, owner)
				seedOldNote(t, db, deckID, "basic", map[string]any{"front": "q", "back": "a"})
			},
		},
		{
			name: "version_14_nullable",
			build: func(t *testing.T, db *gorm.DB) {
				autoMigratePreNoteTypeNotNull(t, db)
				owner := seedUsers(t, db, "note_type_not_null_14")[0]
				deckID := seedDeck(t, db, owner)
				noteType := seedNoteType(t, db, deckID, "Basic")
				now := time.Now().UTC()
				note := Note{
					DeckID: deckID, NoteTypeID: Ptr(noteType.ID),
					FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]",
					CreatedAt: now, UpdatedAt: now,
				}
				if err := db.Create(&note).Error; err != nil {
					t.Fatalf("seed note at version 14: %v", err)
				}
				if err := setVersion(db, 14); err != nil {
					t.Fatalf("set version 14: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for driver, db := range testDatabases(t) {
				t.Run(driver, func(t *testing.T) {
					tc.build(t, db)
					ctx := context.Background()
					if _, err := Sync(ctx, db, BuiltinMigrations); err != nil {
						t.Fatalf("Sync() error = %v", err)
					}
					assertNoteTypeIDNotNull(t, db)
					if _, err := Sync(ctx, db, BuiltinMigrations); err != nil {
						t.Fatalf("second Sync() error = %v", err)
					}
					assertNoteTypeIDNotNull(t, db)
					// The repair must also be safe on a table it already repaired: a rerun of the
					// migration (or a second start that re-executes it) cannot fail.
					if err := notesNoteTypeNotNullRepair(db); err != nil {
						t.Fatalf("repair migration on an already repaired table: %v", err)
					}
					assertNoteTypeIDNotNull(t, db)
					assertNoteWithoutTypeRejected(t, db)
				})
			}
		})
	}
}

// assertNoteTypeIDNotNull reads notes.note_type_id nullability from the database catalog, which is
// what the running database enforces. The GORM model states the intent and is deliberately not
// consulted: a model tag that never reached the column is exactly the regression under test.
func assertNoteTypeIDNotNull(t *testing.T, db *gorm.DB) {
	t.Helper()
	switch db.Dialector.Name() {
	case "sqlite":
		var columns []struct {
			Name    string
			NotNull int `gorm:"column:notnull"`
		}
		if err := db.Raw("PRAGMA table_info(notes)").Scan(&columns).Error; err != nil {
			t.Fatalf("read notes columns: %v", err)
		}
		for _, column := range columns {
			if column.Name != "note_type_id" {
				continue
			}
			if column.NotNull != 1 {
				t.Fatal("notes.note_type_id is nullable in PRAGMA table_info, want NOT NULL")
			}
			return
		}
		t.Fatal("notes.note_type_id is missing from PRAGMA table_info")
	case "postgres":
		var rows []struct {
			IsNullable string `gorm:"column:is_nullable"`
		}
		if err := db.Raw(`SELECT is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'notes' AND column_name = 'note_type_id'`).
			Scan(&rows).Error; err != nil {
			t.Fatalf("read notes.note_type_id from information_schema: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("information_schema has %d rows for notes.note_type_id, want 1", len(rows))
		}
		if rows[0].IsNullable != "NO" {
			t.Fatalf("notes.note_type_id is is_nullable=%q, want NO", rows[0].IsNullable)
		}
	default:
		t.Fatalf("unsupported dialect %q", db.Dialector.Name())
	}
}

// assertNoteWithoutTypeRejected proves the constraint is enforced, not only declared: a raw insert
// that omits note_type_id must fail. The note needs no deck row, because note_type_id is a plain
// column with no foreign key.
func assertNoteWithoutTypeRejected(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	err := db.Exec(`INSERT INTO notes (public_id, deck_id, fields_json, tags_json, created_at, updated_at)
		VALUES (?, ?, '{}', '[]', ?, ?)`, NewPublicID(), 1, now, now).Error
	if err == nil {
		t.Fatal("a note without a note type was inserted, want a NOT NULL violation")
	}
}

// renderAnswerControl replaces the type placeholder on the back with the control's answer breakdown. A template
// without a placeholder (the cloze back) has no control markup to substitute.
func renderAnswerControl(spec notetype.AnswerSpec, face string, fields map[string]string, clozeOrdinal int) (string, error) {
	marker := typeMarkerRE.FindString(face)
	if marker == "" {
		return face, nil
	}
	control, ok := answer.Lookup(spec.Control)
	if !ok {
		return "", fmt.Errorf("unknown answer control %q", spec.Control)
	}
	bindings, err := answer.ResolveBindings(spec, fields)
	if err != nil {
		return "", err
	}
	return answer.RenderSide(control, answer.RenderContext{
		Marker: marker, Side: notetype.SideBack, Bindings: bindings, ClozeOrdinal: clozeOrdinal,
	}, face)
}
