package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/notetype"
)

// The golden fixtures record what Anki 26.09.3 rendered for every card. Importing the package
// and rendering the stored notes with the imported note types must reproduce those faces byte for
// byte: that is the end-to-end proof that the mapping kept fields, templates, CSS and card ordinals
// intact.

const blankFrontMessage = "<div>The front of this card is blank.<br><a href='https://docs.ankiweb.invalid/templates/errors.html#front-of-card-is-blank'>More information</a></div>"

type ankiGolden struct {
	Packages  []string             `json:"packages"`
	NoteTypes []ankiGoldenNoteType `json:"note_types"`
	Notes     []ankiGoldenNote     `json:"notes"`
}

type ankiGoldenNoteType struct {
	Name string `json:"name"`
}

type ankiGoldenNote struct {
	GUID         string            `json:"guid"`
	NoteTypeName string            `json:"notetype_name"`
	Fields       map[string]string `json:"fields"`
	Cards        []ankiGoldenCard  `json:"cards"`
}

type ankiGoldenCard struct {
	ID                int64  `json:"id"`
	Ordinal           int    `json:"ordinal"`
	QuestionNodesText string `json:"question_nodes_text"`
	AnswerNodesText   string `json:"answer_nodes_text"`
	FrontIsEmpty      bool   `json:"front_is_empty"`
}

// ankiFixtures lists every fixture archive and the golden that records its rendered faces. The
// three stock archives share one golden, because they are three exports of the same collection.
var ankiFixtures = []struct {
	archive string
	golden  string
}{
	{"stock.anki2.apkg", "stock"},
	{"stock.anki21.apkg", "stock"},
	{"stock.anki21b.apkg", "stock"},
	{"template-syntax.anki21b.apkg", "template-syntax"},
	{"template-filters.anki21b.apkg", "template-filters"},
	{"cloze.anki21b.apkg", "cloze"},
	{"cardgen.anki21b.apkg", "cardgen"},
	{"audio-tts-latex.anki21b.apkg", "audio-tts-latex"},
	{"typeanswer.anki21b.apkg", "typeanswer"},
	{"notetype-import.anki21b.apkg", "notetype-import"},
}

func ankiFixturePath(name string) string {
	return filepath.Join("..", "notetype", "testdata", "anki", name)
}

func loadAnkiGolden(t *testing.T, name string) ankiGolden {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "notetype", "testdata", "anki", name+".golden.json"))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var golden ankiGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden %s: %v", name, err)
	}
	return golden
}

// TestAnkiImportFixturesRenderLikeGolden imports every fixture archive and compares every rendered
// face with Anki's recorded output.
func TestAnkiImportFixturesRenderLikeGolden(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, fixture := range ankiFixtures {
				fixture := fixture
				t.Run(fixture.archive, func(t *testing.T) {
					golden := loadAnkiGolden(t, fixture.golden)
					raw, err := os.ReadFile(ankiFixturePath(fixture.archive))
					if err != nil {
						t.Fatalf("read fixture: %v", err)
					}
					ctx := context.Background()
					owner := seedUsers(t, db, "anki_"+strings.NewReplacer(".", "_", "-", "_").Replace(fixture.archive))[0]
					report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
						Target:    PackageTargetNewDeck,
						MediaRoot: t.TempDir(),
					})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.NotesCreated != len(golden.Notes) {
						t.Fatalf("notes_created = %d, want %d", report.NotesCreated, len(golden.Notes))
					}
					wantCards := 0
					for _, note := range golden.Notes {
						wantCards += len(note.Cards)
					}
					if report.CardsCreated != wantCards {
						t.Fatalf("cards_created = %d, want %d", report.CardsCreated, wantCards)
					}

					configs := ankiNoteTypeConfigsByName(t, db, report.DeckID)
					for _, note := range golden.Notes {
						cfg, ok := configs[note.NoteTypeName]
						if !ok {
							t.Fatalf("note %q uses unknown note type %q", note.GUID, note.NoteTypeName)
						}
						values := ankiGoldenFieldValues(cfg, note.Fields)
						for _, card := range note.Cards {
							checkAnkiGoldenCard(t, cfg, note, values, card)
						}
					}
				})
			}
		})
	}
}

// TestAnkiImportCreatesOnlyTheNoteTypesAnkiCreates imports the fixture whose package carries a
// note type that no note uses. Anki 26.09.3 creates only the note types its imported notes refer
// to, and the golden records the names it added to an empty collection; the imported deck must
// hold exactly those names.
func TestAnkiImportCreatesOnlyTheNoteTypesAnkiCreates(t *testing.T) {
	const archive = "notetype-import.anki21b.apkg"
	raw, err := os.ReadFile(ankiFixturePath(archive))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var golden struct {
		ImportNoteTypes []string `json:"import_note_types"`
	}
	encoded, err := os.ReadFile(filepath.Join("..", "notetype", "testdata", "anki", "notetype-import.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if len(golden.ImportNoteTypes) == 0 {
		t.Fatal("golden records no imported note types")
	}

	// The fixture's shape is the point of the test: the package must carry an unused note type,
	// otherwise the import would pass without exercising the rule.
	pkg, err := anki.Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	used := make(map[int64]bool, len(pkg.Notes))
	for i := range pkg.Notes {
		used[pkg.Notes[i].NoteTypeID] = true
	}
	unused := 0
	for i := range pkg.NoteTypes {
		if !used[pkg.NoteTypes[i].ID] {
			unused++
		}
	}
	if unused == 0 {
		t.Fatal("fixture package carries no unused note type")
	}

	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_notetype_import")[0]
			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			types, err := NewNoteTypeStore(db).ListByDeck(ctx, report.DeckID)
			if err != nil {
				t.Fatalf("list note types: %v", err)
			}
			names := make([]string, len(types))
			for i := range types {
				names[i] = types[i].Name
			}
			if !slices.Equal(names, golden.ImportNoteTypes) {
				t.Fatalf("imported note types = %v, want %v", names, golden.ImportNoteTypes)
			}
		})
	}
}

// ankiNoteTypeConfigsByName decodes the imported note type configs of a deck by name.
func ankiNoteTypeConfigsByName(t *testing.T, db *gorm.DB, deckID uint64) map[string]notetype.Config {
	t.Helper()
	types, err := NewNoteTypeStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list note types: %v", err)
	}
	out := make(map[string]notetype.Config, len(types))
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			t.Fatalf("decode note type %q: %v", types[i].Name, err)
		}
		out[types[i].Name] = cfg
	}
	return out
}

// ankiGoldenFieldValues maps the golden's field-name keys onto the config's field ids.
func ankiGoldenFieldValues(cfg notetype.Config, fields map[string]string) map[string]string {
	values := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		values[field.ID] = fields[field.Name]
	}
	return values
}

// checkAnkiGoldenCard renders one card with the same data the notetype golden test uses and
// compares it with the recorded question and answer text.
func checkAnkiGoldenCard(t *testing.T, cfg notetype.Config, note ankiGoldenNote, values map[string]string, card ankiGoldenCard) {
	t.Helper()
	var tmpl notetype.Template
	if cfg.Kind == notetype.KindCloze {
		if len(cfg.Templates) == 0 {
			t.Fatalf("%s card %d: cloze note type has no template", note.GUID, card.Ordinal)
		}
		tmpl = cfg.Templates[0]
	} else {
		if card.Ordinal < 0 || card.Ordinal >= len(cfg.Templates) {
			t.Fatalf("%s card %d: template ordinal out of range", note.GUID, card.Ordinal)
		}
		tmpl = cfg.Templates[card.Ordinal]
	}
	data := notetype.RenderData{
		Fields:      values,
		Tags:        []string{"alpha", "beta"},
		TypeName:    note.NoteTypeName,
		DeckName:    "Placeholder::Lesson 1",
		SubdeckName: "Lesson 1",
		CardName:    tmpl.Name,
		CardFlag:    "flag0",
		CardID:      strconv.FormatInt(card.ID, 10),
	}
	if cfg.Kind == notetype.KindCloze {
		data.ClozeOrdinal = card.Ordinal + 1
	}
	front, err := notetype.Render(cfg, tmpl, notetype.SideFront, data)
	if err != nil {
		t.Fatalf("%s card %d: front: %v", note.GUID, card.Ordinal, err)
	}
	back, err := notetype.Render(cfg, tmpl, notetype.SideBack, data)
	if err != nil {
		t.Fatalf("%s card %d: back: %v", note.GUID, card.Ordinal, err)
	}
	if card.FrontIsEmpty && cfg.Kind == notetype.KindNormal {
		wantFront, found := strings.CutSuffix(card.QuestionNodesText, blankFrontMessage)
		if !found {
			t.Fatalf("%s card %d: golden question does not end with the blank-front message", note.GUID, card.Ordinal)
		}
		if front != wantFront {
			t.Errorf("%s card %d front:\n got %q\nwant %q", note.GUID, card.Ordinal, front, wantFront)
		}
		if card.AnswerNodesText != blankFrontMessage {
			t.Errorf("%s card %d: golden answer is not the blank-front message", note.GUID, card.Ordinal)
		}
		return
	}
	if front != card.QuestionNodesText {
		t.Errorf("%s card %d front:\n got %q\nwant %q", note.GUID, card.Ordinal, front, card.QuestionNodesText)
	}
	if back != card.AnswerNodesText {
		t.Errorf("%s card %d back:\n got %q\nwant %q", note.GUID, card.Ordinal, back, card.AnswerNodesText)
	}
}

// TestAnkiReimportKeepsProgress proves the re-import contract: notes are matched by guid, fields
// are refreshed, and the cards and their per-user progress survive.
func TestAnkiReimportKeepsProgress(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_reimport_"+driver)[0]
			raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			store := NewDeckStore(db)
			first, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("first import: %v", err)
			}

			var note Note
			if err := db.Where("deck_id = ?", first.DeckID).Order("id ASC").First(&note).Error; err != nil {
				t.Fatalf("load note: %v", err)
			}
			var card Card
			if err := db.Where("note_id = ?", note.ID).Order("id ASC").First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			due := time.Now().UTC()
			state := CardState{
				CardID: card.ID, UserID: owner, State: "review", DueAt: &due,
				Stability: Ptr(12.5), Difficulty: Ptr(4.25), Reps: 7, Lapses: 1,
				ScheduledDays: 12, ElapsedDays: 3, Version: 4,
			}
			if err := db.Create(&state).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}
			// Also change the note's content: a re-import must restore the package's field values.
			if err := db.Model(&Note{}).Where("id = ?", note.ID).Update("fields_json", `{"f0":"stale","f1":"stale"}`).Error; err != nil {
				t.Fatalf("stale note: %v", err)
			}

			second, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    "into_deck:" + strconv.FormatUint(first.DeckID, 10),
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("re-import: %v", err)
			}
			if second.NotesCreated != 0 || second.NotesUpdated == 0 {
				t.Fatalf("re-import report = %+v, want updates and no creates", second)
			}
			if second.CardsCreated != 0 {
				t.Fatalf("re-import created %d cards, want 0", second.CardsCreated)
			}

			var stateAfter CardState
			if err := db.Where("card_id = ? AND user_id = ?", card.ID, owner).First(&stateAfter).Error; err != nil {
				t.Fatalf("load card state: %v", err)
			}
			if stateAfter.State != "review" || stateAfter.Reps != 7 || stateAfter.Version != 4 || stateAfter.Stability == nil || *stateAfter.Stability != 12.5 {
				t.Fatalf("progress after re-import = %+v, want the original state kept", stateAfter)
			}
			var noteAfter Note
			if err := db.Where("id = ?", note.ID).First(&noteAfter).Error; err != nil {
				t.Fatalf("load note after re-import: %v", err)
			}
			if strings.Contains(noteAfter.FieldsJSON, "stale") {
				t.Fatalf("note fields were not refreshed: %s", noteAfter.FieldsJSON)
			}
			var cardCount int64
			db.Model(&Card{}).Where("note_id = ?", note.ID).Count(&cardCount)
			if cardCount == 0 {
				t.Fatal("re-import removed every card of the note")
			}
		})
	}
}

// TestAnkiReimportRestoresDeletedNote covers the into_deck case: after the user deleted one imported
// note, re-importing the same package must restore that row instead of violating the unique
// (deck_id, external_ref) index. The restored note keeps its id, and its card keeps its progress.
func TestAnkiReimportRestoresDeletedNote(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_restore_"+driver)[0]
			raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			store := NewDeckStore(db)
			first, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("first import: %v", err)
			}

			var note Note
			if err := db.Where("deck_id = ?", first.DeckID).Order("id ASC").First(&note).Error; err != nil {
				t.Fatalf("load note: %v", err)
			}
			var card Card
			if err := db.Where("note_id = ?", note.ID).Order("id ASC").First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			due := time.Now().UTC()
			state := CardState{
				CardID: card.ID, UserID: owner, State: "review", DueAt: &due,
				Reps: 5, ScheduledDays: 5, Version: 2,
			}
			if err := db.Create(&state).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}
			if err := NewNoteStore(db).Delete(ctx, note.ID); err != nil {
				t.Fatalf("delete note: %v", err)
			}

			second, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    "into_deck:" + strconv.FormatUint(first.DeckID, 10),
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("re-import after delete: %v", err)
			}
			if second.NotesCreated != 0 || second.NotesUpdated == 0 {
				t.Fatalf("re-import report = %+v, want updates and no creates", second)
			}

			var restored Note
			if err := db.Unscoped().Where("id = ?", note.ID).First(&restored).Error; err != nil {
				t.Fatalf("load note %d: %v", note.ID, err)
			}
			if restored.DeletedAt.Valid {
				t.Fatalf("note %d is still soft-deleted after the re-import", note.ID)
			}
			var sameRef int64
			if err := db.Unscoped().Model(&Note{}).
				Where("deck_id = ? AND external_ref = ?", first.DeckID, *note.ExternalRef).
				Count(&sameRef).Error; err != nil {
				t.Fatalf("count notes by ref: %v", err)
			}
			if sameRef != 1 {
				t.Fatalf("%d notes carry the ref %q, want the single restored row", sameRef, *note.ExternalRef)
			}

			var stateAfter CardState
			if err := db.Where("card_id = ? AND user_id = ?", card.ID, owner).First(&stateAfter).Error; err != nil {
				t.Fatalf("card %d lost its progress after the re-import: %v", card.ID, err)
			}
			if stateAfter.Reps != 5 || stateAfter.Version != 2 {
				t.Fatalf("progress after re-import = %+v, want the original state kept", stateAfter)
			}
		})
	}
}

// TestAnkiReplaceDeckRestoresNotes covers the replace_deck case: the deck already holds the package,
// and replace_deck soft-deletes every note before writing the same guids back. Every note must be
// restored with its id, and its cards must keep their progress.
func TestAnkiReplaceDeckRestoresNotes(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_replace_"+driver)[0]
			raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			store := NewDeckStore(db)
			first, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("first import: %v", err)
			}

			var before []Note
			if err := db.Where("deck_id = ?", first.DeckID).Order("id ASC").Find(&before).Error; err != nil {
				t.Fatalf("load notes: %v", err)
			}
			if len(before) == 0 {
				t.Fatal("first import wrote no notes")
			}
			ids := make(map[string]uint64, len(before))
			for i := range before {
				ids[*before[i].ExternalRef] = before[i].ID
			}
			var card Card
			if err := db.Where("note_id = ?", before[0].ID).Order("id ASC").First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			due := time.Now().UTC()
			state := CardState{
				CardID: card.ID, UserID: owner, State: "review", DueAt: &due,
				Reps: 3, ScheduledDays: 2, Version: 1,
			}
			if err := db.Create(&state).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}

			second, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
				Target:    "replace_deck:" + strconv.FormatUint(first.DeckID, 10),
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("replace import over an existing package: %v", err)
			}
			if second.NotesCreated != 0 || second.NotesUpdated != len(before) {
				t.Fatalf("replace report = %+v, want %d updates and no creates", second, len(before))
			}

			var after []Note
			if err := db.Where("deck_id = ?", first.DeckID).Order("id ASC").Find(&after).Error; err != nil {
				t.Fatalf("load notes after replace: %v", err)
			}
			if len(after) != len(before) {
				t.Fatalf("replace left %d notes, want %d", len(after), len(before))
			}
			for i := range after {
				if after[i].ExternalRef == nil {
					t.Fatalf("note %d lost its external ref", after[i].ID)
				}
				if want, ok := ids[*after[i].ExternalRef]; !ok || want != after[i].ID {
					t.Fatalf("note with ref %q has id %d, want %d", *after[i].ExternalRef, after[i].ID, want)
				}
			}

			var stateAfter CardState
			if err := db.Where("card_id = ? AND user_id = ?", card.ID, owner).First(&stateAfter).Error; err != nil {
				t.Fatalf("card %d lost its progress after the replace: %v", card.ID, err)
			}
			if stateAfter.Reps != 3 || stateAfter.Version != 1 {
				t.Fatalf("progress after replace = %+v, want the original state kept", stateAfter)
			}
		})
	}
}

// TestAnkiDryRunWritesNothing proves dry_run counts the package without leaving a row or a file.
func TestAnkiDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := packageDatabases(t)["sqlite"]
	owner := seedUsers(t, db, "anki_dry")[0]
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mediaRoot := t.TempDir()
	report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
		Target: PackageTargetNewDeck, DryRun: true, MediaRoot: mediaRoot,
	})
	if err != nil {
		t.Fatalf("dry-run import: %v", err)
	}
	if !report.DryRun || report.NotesCreated != 8 {
		t.Fatalf("dry-run report = %+v, want dry_run with eight notes", report)
	}
	for _, table := range []any{&Deck{}, &Preset{}, &NoteType{}, &Note{}, &Card{}, &Media{}, &MediaName{}} {
		var count int64
		if err := db.Model(table).Count(&count).Error; err != nil {
			t.Fatalf("count table: %v", err)
		}
		if count != 0 {
			t.Fatalf("dry run wrote %d rows into %T", count, table)
		}
	}
	if entries, err := os.ReadDir(mediaRoot); err == nil && len(entries) != 0 {
		t.Fatalf("dry run wrote %d entries under the media root", len(entries))
	}
}

// TestAnkiReportItems covers the four report lists with a hand-built package model.
func TestAnkiReportItems(t *testing.T) {
	pkg := &anki.Package{
		NoteTypes: []anki.NoteType{{
			ID: 1, Name: "Test", Kind: anki.KindNormal,
			Fields:    []anki.Field{{Ord: 0, Name: "Front"}},
			Templates: []anki.Template{{Ord: 0, Name: "Card 1", QFormat: `<script src="https://example.com/script.js"></script>{{Front}}`, AFormat: `<link rel="stylesheet" href="https://example.com/style.css">{{Front}}`}},
		}},
		Notes: []anki.Note{{
			ID: 10, GUID: "g", NoteTypeID: 1,
			Fields: []string{`<img src="missing.png"> [sound:gone.mp3] [latex]x^2[/latex] <script src="https://example.com/note.js"></script>`},
		}},
		Media: []anki.MediaEntry{
			{Name: "clip.mp4", Data: []byte("not really an mp4")},
			{Name: "movie.avi", Data: []byte("RIFF....AVI LIST")},
			{Name: "clip.webm", Data: []byte("not really a webm")},
		},
	}
	missing := ankiMissingMedia(pkg)
	wantMissing := []string{"gone.mp3", "missing.png"}
	if strings.Join(missing, ",") != strings.Join(wantMissing, ",") {
		t.Fatalf("missing media = %v, want %v", missing, wantMissing)
	}
	video := ankiUnplayableVideo(pkg)
	if len(video) != 1 || video[0] != "movie.avi" {
		t.Fatalf("unplayable video = %v, want only movie.avi", video)
	}
	assets := ankiExternalAssets(pkg)
	sort.Strings(assets)
	wantAssets := []string{"https://example.com/note.js", "https://example.com/script.js", "https://example.com/style.css"}
	if strings.Join(assets, ",") != strings.Join(wantAssets, ",") {
		t.Fatalf("external assets = %v, want %v", assets, wantAssets)
	}
	types := map[int64]anki.NoteType{1: pkg.NoteTypes[0]}
	latex := ankiMissingLatexImages(pkg, types)
	if len(latex) != 1 || !strings.HasPrefix(latex[0], "latex-") {
		t.Fatalf("missing LaTeX images = %v, want one latex-… name", latex)
	}
}

// TestIsAnkiArchiveSniffsEntries proves the .apkg/.edeck decision reads entry names, not the suffix.
func TestIsAnkiArchiveSniffsEntries(t *testing.T) {
	apkg, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !isAnkiArchive(apkg) {
		t.Fatal("an .apkg was not recognised")
	}
	if isAnkiArchive(minimalPackageZipForTest(t)) {
		t.Fatal("an .edeck was recognised as an Anki package")
	}
	if isAnkiArchive([]byte("not a zip")) {
		t.Fatal("garbage was recognised as an Anki package")
	}
}

// minimalPackageZipForTest builds the smallest .edeck-shaped zip: manifest.json only.
func minimalPackageZipForTest(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLocalAnkiPackage is the maintainer's manual acceptance hook for a package that cannot be
// committed (the Ultimate Geography deck lives in testdata/local/ and is gitignored). It imports
// every local .apkg and prints the deck's note/card count plus the SHA-256 of every rendered face.
//
// Set ENGRAM_ANKI_ORACLE_CARDS and ENGRAM_ANKI_ORACLE_SHA256 from the Anki oracle run to turn the
// printout into an assertion. Without a local package the test skips, so CI never depends on it.
func TestLocalAnkiPackage(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "local", "*.apkg"))
	if err != nil {
		t.Fatalf("glob local packages: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no local .apkg under testdata/local")
	}
	sort.Strings(files)
	ctx := context.Background()
	db := packageDatabases(t)["sqlite"]
	owner := seedUsers(t, db, "anki_local")[0]
	for i, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
			Target: PackageTargetNewDeck, MediaRoot: t.TempDir(),
			NewDeckName: fmt.Sprintf("local-%d", i+1),
		})
		if err != nil {
			t.Fatalf("%s: import: %v", file, err)
		}
		cards, faces := ankiDeckFaceDigest(t, db, report.DeckID)
		sum := fmt.Sprintf("%x", sha256Of(strings.Join(faces, "\n")))
		t.Logf("%s: notes=%d cards=%d faces_sha256=%s", filepath.Base(file), report.NotesCreated, cards, sum)
		if want := os.Getenv("ENGRAM_ANKI_ORACLE_CARDS"); want != "" && want != strconv.Itoa(cards) {
			t.Fatalf("%s: cards = %d, oracle says %s", filepath.Base(file), cards, want)
		}
		if want := os.Getenv("ENGRAM_ANKI_ORACLE_SHA256"); want != "" && want != sum {
			t.Fatalf("%s: faces = %s, oracle says %s", filepath.Base(file), sum, want)
		}
	}
}

// TestLocalUltimateGeographyDeckName is the acceptance check for the top-deck rule on a real
// package. The Ultimate Geography archive carries Anki's empty "Default" deck next to the deck
// that holds its cards. Importing it without NewDeckName must name the engram deck after the
// populated deck and put every card directly in it, never below a package deck path. The archive
// is gitignored, so the test skips when it is absent.
func TestLocalUltimateGeographyDeckName(t *testing.T) {
	const cardCount = 975
	file := filepath.Join("..", "..", "testdata", "local", "Ultimate-Geography.apkg")
	raw, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("no %s", file)
		}
		t.Fatalf("read %s: %v", file, err)
	}
	ctx := context.Background()
	db := packageDatabases(t)["sqlite"]
	owner := seedUsers(t, db, "anki_ug")[0]
	report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
		Target: PackageTargetNewDeck, MediaRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("import %s: %v", file, err)
	}
	deck, err := NewDeckStore(db).ByID(ctx, report.DeckID)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	if deck.Name != "Ultimate Geography" {
		t.Fatalf("deck name = %q, want %q", deck.Name, "Ultimate Geography")
	}
	var total, withPath int64
	if err := db.Model(&Card{}).Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", report.DeckID).Count(&total).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if err := db.Model(&Card{}).Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ? AND cards.deck_path IS NOT NULL", report.DeckID).Count(&withPath).Error; err != nil {
		t.Fatalf("count cards with a deck path: %v", err)
	}
	if total != cardCount {
		t.Fatalf("imported %d cards, want %d", total, cardCount)
	}
	if withPath != 0 {
		t.Fatalf("%d of %d cards carry a deck path, want 0", withPath, total)
	}
	t.Logf("deck %q: %d cards, %d with a deck path", deck.Name, total, withPath)
}

// ankiDeckFaceDigest renders every card of the deck and returns the card count plus one line per
// face ("guid|ordinal|front|back"). The line format is the evidence format the report documents.
func ankiDeckFaceDigest(t *testing.T, db *gorm.DB, deckID uint64) (int, []string) {
	t.Helper()
	ctx := context.Background()
	deck, err := NewDeckStore(db).ByID(ctx, deckID)
	if err != nil {
		t.Fatalf("load deck: %v", err)
	}
	types, err := NewNoteTypeStore(db).ListByDeck(ctx, deckID)
	if err != nil {
		t.Fatalf("list note types: %v", err)
	}
	configs := make(map[uint64]notetype.Config, len(types))
	names := make(map[uint64]string, len(types))
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			t.Fatalf("decode note type %d: %v", types[i].ID, err)
		}
		configs[types[i].ID] = cfg
		names[types[i].ID] = types[i].Name
	}
	var notes []Note
	if err := db.Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		t.Fatalf("list notes: %v", err)
	}
	var faces []string
	total := 0
	for i := range notes {
		note := &notes[i]
		if note.NoteTypeID == nil {
			continue
		}
		cfg, ok := configs[*note.NoteTypeID]
		if !ok {
			t.Fatalf("note %d names unknown note type %d", note.ID, *note.NoteTypeID)
		}
		values := NoteFieldValues(note.FieldsJSON)
		var rows []Card
		if err := db.Where("note_id = ?", note.ID).Order("ordinal ASC, id ASC").Find(&rows).Error; err != nil {
			t.Fatalf("list cards of note %d: %v", note.ID, err)
		}
		for _, card := range rows {
			total++
			tmpl := cardTemplateForOrdinal(cfg, card.Ordinal)
			deckName := deck.Name
			subdeck := deck.Name
			if card.DeckPath != nil {
				deckName = deck.Name + "::" + *card.DeckPath
				subdeck = path.Base(*card.DeckPath)
			}
			data := notetype.RenderData{
				Fields: values, Tags: TagsOrEmpty(note.TagsJSON), TypeName: names[*note.NoteTypeID],
				DeckName: deckName, SubdeckName: subdeck, CardName: tmpl.Name,
				CardFlag: "flag0", CardID: strconv.FormatUint(card.ID, 10),
			}
			if cfg.Kind == notetype.KindCloze {
				data.ClozeOrdinal = card.Ordinal + 1
			}
			front, err := notetype.Render(cfg, tmpl, notetype.SideFront, data)
			if err != nil {
				t.Fatalf("render front of note %d: %v", note.ID, err)
			}
			back, err := notetype.Render(cfg, tmpl, notetype.SideBack, data)
			if err != nil {
				t.Fatalf("render back of note %d: %v", note.ID, err)
			}
			guid := ""
			if note.ExternalRef != nil {
				guid = strings.TrimPrefix(*note.ExternalRef, "anki:")
			}
			faces = append(faces, fmt.Sprintf("%s|%d|%s|%s", guid, card.Ordinal, front, back))
		}
	}
	return total, faces
}

// cardTemplateForOrdinal mirrors the card model's identity rule: a cloze card uses the single
// template, a normal card uses the template at its ordinal.
func cardTemplateForOrdinal(cfg notetype.Config, ordinal int) notetype.Template {
	if cfg.Kind == notetype.KindCloze {
		return cfg.Templates[0]
	}
	return cfg.Templates[ordinal]
}

// sha256Of returns the digest of the canonical face dump.
func sha256Of(value string) [32]byte { return sha256.Sum256([]byte(value)) }

// TestAnkiReimportStructuralChangeMovesNotes covers the other half of the note type rule: when the
// stored structure no longer matches the package's field/template names and counts, a new note
// type is created and the matched notes move onto it, keeping their cards. A second re-import
// must reuse the newest type for that Anki origin instead of creating another one.
func TestAnkiReimportStructuralChangeMovesNotes(t *testing.T) {
	ctx := context.Background()
	db := packageDatabases(t)["sqlite"]
	owner := seedUsers(t, db, "anki_struct")[0]
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	store := NewDeckStore(db)
	first, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
		Target: PackageTargetNewDeck, MediaRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	types, err := NewNoteTypeStore(db).ListByDeck(ctx, first.DeckID)
	if err != nil {
		t.Fatalf("list note types: %v", err)
	}
	var original NoteType
	var originalCfg notetype.Config
	originID := int64(0)
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			t.Fatalf("decode note type: %v", err)
		}
		if cfg.AnkiOrigin != nil && len(cfg.Fields) == 2 {
			original = types[i]
			originalCfg = cfg
			originID = cfg.AnkiOrigin.NoteTypeID
			break
		}
	}
	if original.ID == 0 {
		t.Fatal("no two-field imported note type found")
	}
	var note Note
	if err := db.Where("deck_id = ? AND note_type_id = ?", first.DeckID, original.ID).First(&note).Error; err != nil {
		t.Fatalf("load note: %v", err)
	}
	var card Card
	if err := db.Where("note_id = ?", note.ID).Order("id ASC").First(&card).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}

	// Change the stored structure: drop the second field and its template's need for it stays
	// valid because only the front template is validated.
	changed := originalCfg
	changed.Fields = changed.Fields[:1]
	rawCfg, err := json.Marshal(changed)
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	if err := db.Model(&NoteType{}).Where("id = ?", original.ID).Update("config_json", string(rawCfg)).Error; err != nil {
		t.Fatalf("change note type: %v", err)
	}

	for round := 0; round < 2; round++ {
		if _, err := store.ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
			Target: "into_deck:" + strconv.FormatUint(first.DeckID, 10), MediaRoot: t.TempDir(),
		}); err != nil {
			t.Fatalf("re-import %d: %v", round+1, err)
		}
		var noteAfter Note
		if err := db.Where("id = ?", note.ID).First(&noteAfter).Error; err != nil {
			t.Fatalf("load moved note: %v", err)
		}
		if noteAfter.NoteTypeID == nil || *noteAfter.NoteTypeID == original.ID {
			t.Fatalf("re-import %d did not move the note to a new note type", round+1)
		}
		var cardAfter Card
		if err := db.Where("id = ?", card.ID).First(&cardAfter).Error; err != nil {
			t.Fatalf("card lost after structural re-import: %v", err)
		}
		matches := 0
		for _, nt := range mustNoteTypes(t, db, first.DeckID) {
			cfg, err := nt.Config()
			if err != nil {
				continue
			}
			if cfg.AnkiOrigin != nil && cfg.AnkiOrigin.NoteTypeID == originID {
				matches++
			}
		}
		if matches != 2 {
			t.Fatalf("round %d: %d note types for origin %d, want 2", round+1, matches, originID)
		}
	}
}

// mustNoteTypes lists a deck's note types or fails.
func mustNoteTypes(t *testing.T, db *gorm.DB, deckID uint64) []NoteType {
	t.Helper()
	types, err := NewNoteTypeStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list note types: %v", err)
	}
	return types
}

// TestPlanAnkiDecks covers the top-deck rule on constructed packages: only decks that hold cards
// take part, the top name is the longest shared "::" prefix (or the largest branch's first
// component), a deck under the top name becomes a relative path, and a deck outside the chosen
// tree keeps its own name as the path.
func TestPlanAnkiDecks(t *testing.T) {
	cases := []struct {
		name            string
		decks           []anki.Deck
		cardDecks       []int64
		wantTop         string
		wantDescription string
		wantPaths       map[int64]string
	}{
		{
			// Anki's packaged "Default" deck holds no card and must not name the engram deck.
			name: "empty Default deck with one populated deck",
			decks: []anki.Deck{
				{ID: 1, Name: "Default", Description: "packaged default"},
				{ID: 1001, Name: "Ultimate Geography", Description: "geography notes"},
			},
			cardDecks:       []int64{1001, 1001, 1001},
			wantTop:         "Ultimate Geography",
			wantDescription: "geography notes",
			wantPaths:       map[int64]string{1: "Default", 1001: ""},
		},
		{
			// Cards only below a common parent: the parent is the top deck even though it holds
			// no card of its own.
			name: "tree whose cards sit only in the subdecks",
			decks: []anki.Deck{
				{ID: 1, Name: "A", Description: "deck A"},
				{ID: 2, Name: "A::B"},
				{ID: 3, Name: "A::C"},
			},
			cardDecks:       []int64{2, 2, 3},
			wantTop:         "A",
			wantDescription: "deck A",
			wantPaths:       map[int64]string{1: "", 2: "B", 3: "C"},
		},
		{
			// The longest shared prefix wins over the shortest populated name.
			name: "shared prefix nested below the root",
			decks: []anki.Deck{
				{ID: 1, Name: "A"},
				{ID: 2, Name: "A::B"},
				{ID: 3, Name: "A::B::C"},
			},
			cardDecks:       []int64{2, 3},
			wantTop:         "A::B",
			wantDescription: "",
			wantPaths:       map[int64]string{1: "A", 2: "", 3: "C"},
		},
		{
			// No shared component: the branch holding the most cards names the deck.
			name: "unrelated decks",
			decks: []anki.Deck{
				{ID: 1, Name: "A"},
				{ID: 2, Name: "B", Description: "deck B"},
			},
			cardDecks:       []int64{1, 1, 2, 2, 2},
			wantTop:         "B",
			wantDescription: "deck B",
			wantPaths:       map[int64]string{1: "A", 2: ""},
		},
		{
			// Equal counts: the lowest deck id wins, not the package order.
			name: "unrelated decks with an equal card count",
			decks: []anki.Deck{
				{ID: 7, Name: "B"},
				{ID: 3, Name: "A"},
			},
			cardDecks:       []int64{7, 7, 3, 3},
			wantTop:         "A",
			wantDescription: "",
			wantPaths:       map[int64]string{3: "", 7: "B"},
		},
		{
			// The packaged "Default" deck may hold cards; when it does, it competes on equal
			// terms and wins here because it holds more.
			name: "Default deck holding cards",
			decks: []anki.Deck{
				{ID: 1, Name: "Default"},
				{ID: 2, Name: "Other::Sub"},
			},
			cardDecks:       []int64{1, 1, 1, 2},
			wantTop:         "Default",
			wantDescription: "",
			wantPaths:       map[int64]string{1: "", 2: "Other::Sub"},
		},
		{
			// A description the deck model would reject is dropped, not allowed to fail the
			// import: real Anki decks carry multi-line HTML blurbs over the length bound.
			name: "unstorable description is dropped",
			decks: []anki.Deck{
				{ID: 1, Name: "A", Description: strings.Repeat("x", 2001)},
				{ID: 2, Name: "B", Description: "line\nbreak"},
			},
			cardDecks:       []int64{1, 2, 2},
			wantTop:         "B",
			wantDescription: "",
			wantPaths:       map[int64]string{1: "A", 2: ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg := &anki.Package{Decks: tc.decks}
			for i, deckID := range tc.cardDecks {
				pkg.Cards = append(pkg.Cards, anki.Card{ID: int64(i + 1), DeckID: deckID})
			}
			plan := planAnkiDecks(pkg)
			if plan.TopName != tc.wantTop {
				t.Errorf("top deck = %q, want %q", plan.TopName, tc.wantTop)
			}
			if plan.Description != tc.wantDescription {
				t.Errorf("description = %q, want %q", plan.Description, tc.wantDescription)
			}
			if len(plan.Paths) != len(tc.wantPaths) {
				t.Errorf("planned %d deck paths, want %d: %v", len(plan.Paths), len(tc.wantPaths), plan.Paths)
			}
			for id, want := range tc.wantPaths {
				if got := plan.Paths[id]; got != want {
					t.Errorf("deck %d path = %q, want %q", id, got, want)
				}
			}
		})
	}
}

// TestAnkiImportSubdeckPaths proves the subdeck rule reaches the card rows: a card below the top
// deck carries a relative "::" path, and the modern layout's unit-separator spelling never leaks
// into the stored path.
func TestAnkiImportSubdeckPaths(t *testing.T) {
	ctx := context.Background()
	db := packageDatabases(t)["sqlite"]
	owner := seedUsers(t, db, "anki_paths")[0]
	raw, err := os.ReadFile(ankiFixturePath("template-syntax.anki21b.apkg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{
		Target: PackageTargetNewDeck, MediaRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var cards []Card
	if err := db.Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", report.DeckID).Find(&cards).Error; err != nil {
		t.Fatalf("load cards: %v", err)
	}
	withPath := 0
	for i := range cards {
		if cards[i].DeckPath == nil {
			continue
		}
		withPath++
		if strings.Contains(*cards[i].DeckPath, "\x1f") {
			t.Fatalf("stored deck path still carries the unit separator: %q", *cards[i].DeckPath)
		}
	}
	if withPath == 0 {
		t.Fatal("no card kept a subdeck path")
	}
}

// ankiModelsWithoutLastTemplate drops the last template of one legacy note type and returns the
// rewritten col.models JSON plus the ordinal it dropped.
func ankiModelsWithoutLastTemplate(t *testing.T, db *gorm.DB, modelID int64) (string, int) {
	t.Helper()
	var modelsJSON string
	if err := db.Raw("SELECT models FROM col").Scan(&modelsJSON).Error; err != nil {
		t.Fatalf("read col.models: %v", err)
	}
	var models map[string]json.RawMessage
	if err := json.Unmarshal([]byte(modelsJSON), &models); err != nil {
		t.Fatalf("decode col.models: %v", err)
	}
	for key, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil {
			t.Fatalf("decode model %s: %v", key, err)
		}
		var id int64
		if err := json.Unmarshal(model["id"], &id); err != nil || id != modelID {
			continue
		}
		var templates []json.RawMessage
		if err := json.Unmarshal(model["tmpls"], &templates); err != nil {
			t.Fatalf("decode templates of model %d: %v", modelID, err)
		}
		if len(templates) < 2 {
			t.Fatalf("model %d has %d templates, want at least two", modelID, len(templates))
		}
		dropped := len(templates) - 1
		encodedTemplates, err := json.Marshal(templates[:dropped])
		if err != nil {
			t.Fatalf("encode templates of model %d: %v", modelID, err)
		}
		model["tmpls"] = encodedTemplates
		encoded, err := json.Marshal(model)
		if err != nil {
			t.Fatalf("encode model %d: %v", modelID, err)
		}
		models[key] = encoded
		encodedModels, err := json.Marshal(models)
		if err != nil {
			t.Fatalf("encode col.models: %v", err)
		}
		return string(encodedModels), dropped
	}
	t.Fatalf("model %d is not in the fixture collection", modelID)
	return "", 0
}

// ankiFixtureWithoutTemplate rebuilds a legacy fixture whose note type lost its last template.
// Deleting a template in Anki takes the cards of that template with it, so the cards of the dropped
// ordinal are removed as well and the package stays self-consistent: the only change left is the
// note type's structure.
func ankiFixtureWithoutTemplate(t *testing.T, modelID int64) []byte {
	t.Helper()
	raw, err := os.ReadFile(ankiFixturePath("stock.anki2.apkg"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	entries := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = data
	}
	collection, ok := entries["collection.anki2"]
	if !ok {
		t.Fatal("fixture carries no collection.anki2")
	}

	// The collection is a SQLite database; it is edited in a temporary file, never in the fixture.
	file := filepath.Join(t.TempDir(), "collection.anki2")
	if err := os.WriteFile(file, collection, 0o600); err != nil {
		t.Fatalf("write collection: %v", err)
	}
	collectionDB, err := gorm.Open(sqlite.Open(file), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open collection: %v", err)
	}
	modelsJSON, dropped := ankiModelsWithoutLastTemplate(t, collectionDB, modelID)
	if err := collectionDB.Exec("UPDATE col SET models = ?", modelsJSON).Error; err != nil {
		t.Fatalf("write col.models: %v", err)
	}
	if err := collectionDB.Exec("DELETE FROM cards WHERE ord = ? AND nid IN (SELECT id FROM notes WHERE mid = ?)", dropped, modelID).Error; err != nil {
		t.Fatalf("delete cards of ordinal %d: %v", dropped, err)
	}
	sqlDB, err := collectionDB.DB()
	if err != nil {
		t.Fatalf("collection handle: %v", err)
	}
	// The bytes are read back only after the handle is closed, so every write is flushed to the file.
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close collection: %v", err)
	}
	collection, err = os.ReadFile(file)
	if err != nil {
		t.Fatalf("read rewritten collection: %v", err)
	}
	entries["collection.anki2"] = collection

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := w.Write(entries[name]); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return buf.Bytes()
}

// TestAnkiStructuralReimportRemovesStaleTemplateCards proves a moved note does not keep a card its
// new note type cannot render. The re-imported package lost one template, so the note type's
// structure changed and the note moved to a new row; the stored card of the dropped template keeps
// the old type's template id, which no longer exists. That card is soft-deleted, its card_states row
// stays, the report counts it, and every remaining card of the deck renders.
func TestAnkiStructuralReimportRemovesStaleTemplateCards(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_stale_"+driver)[0]
			original, err := os.ReadFile(ankiFixturePath("stock.anki2.apkg"))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			changed := ankiFixtureWithoutTemplate(t, 1001)

			store := NewDeckStore(db)
			first, err := store.ImportPackage(ctx, owner, bytes.NewReader(original), PackageImportOptions{
				Target:    PackageTargetNewDeck,
				MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("first import: %v", err)
			}

			// The note type that loses a template, and the single note that holds its two cards.
			types, err := NewNoteTypeStore(db).ListByDeck(ctx, first.DeckID)
			if err != nil {
				t.Fatalf("list note types: %v", err)
			}
			var typeID uint64
			for i := range types {
				if types[i].Name == "Basic (and reversed card)" {
					typeID = types[i].ID
				}
			}
			if typeID == 0 {
				t.Fatal("the imported deck has no note type called Basic (and reversed card)")
			}
			var note Note
			if err := db.Where("deck_id = ? AND note_type_id = ?", first.DeckID, typeID).First(&note).Error; err != nil {
				t.Fatalf("load note: %v", err)
			}
			var cards []Card
			if err := db.Where("note_id = ?", note.ID).Order("ordinal ASC").Find(&cards).Error; err != nil {
				t.Fatalf("load cards: %v", err)
			}
			if len(cards) != 2 {
				t.Fatalf("note %d has %d cards, want 2", note.ID, len(cards))
			}
			dropped, surviving := cards[1], cards[0]
			state := CardState{CardID: dropped.ID, UserID: owner, State: "review", Reps: 3, Version: 1}
			if err := db.Create(&state).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}

			target := "into_deck:" + strconv.FormatUint(first.DeckID, 10)
			dry, err := store.ImportPackage(ctx, owner, bytes.NewReader(changed), PackageImportOptions{
				Target: target, DryRun: true, MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("dry-run re-import: %v", err)
			}
			if dry.CardsRemoved != 1 {
				t.Fatalf("dry run cards_removed = %d, want 1", dry.CardsRemoved)
			}
			var liveAfterDry int64
			if err := db.Model(&Card{}).Where("id = ?", dropped.ID).Count(&liveAfterDry).Error; err != nil {
				t.Fatalf("count card after dry run: %v", err)
			}
			if liveAfterDry != 1 {
				t.Fatal("the dry run soft-deleted the card")
			}

			second, err := store.ImportPackage(ctx, owner, bytes.NewReader(changed), PackageImportOptions{
				Target: target, MediaRoot: t.TempDir(),
			})
			if err != nil {
				t.Fatalf("re-import: %v", err)
			}
			if second.CardsRemoved != 1 {
				t.Fatalf("cards_removed = %d, want 1", second.CardsRemoved)
			}

			var removed Card
			if err := db.Unscoped().First(&removed, dropped.ID).Error; err != nil {
				t.Fatalf("load removed card: %v", err)
			}
			if !removed.DeletedAt.Valid {
				t.Fatalf("card %d of the dropped template is still live", dropped.ID)
			}
			var stateAfter CardState
			if err := db.Where("card_id = ? AND user_id = ?", dropped.ID, owner).First(&stateAfter).Error; err != nil {
				t.Fatalf("the removed card lost its card state: %v", err)
			}
			if stateAfter.Reps != 3 || stateAfter.Version != 1 {
				t.Fatalf("card state after re-import = %+v, want the original state kept", stateAfter)
			}
			var kept Card
			if err := db.First(&kept, surviving.ID).Error; err != nil {
				t.Fatalf("the surviving card of the same note was removed: %v", err)
			}

			assertDeckCardsRender(t, db, first.DeckID)
		})
	}
}

// assertDeckCardsRender renders the front of every live card of a deck, failing when a card's
// template is not declared by its note type.
func assertDeckCardsRender(t *testing.T, db *gorm.DB, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	types, err := NewNoteTypeStore(db).ListByDeck(ctx, deckID)
	if err != nil {
		t.Fatalf("list note types: %v", err)
	}
	cfgByID := make(map[uint64]notetype.Config, len(types))
	nameByID := make(map[uint64]string, len(types))
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			t.Fatalf("decode note type %q: %v", types[i].Name, err)
		}
		cfgByID[types[i].ID] = cfg
		nameByID[types[i].ID] = types[i].Name
	}
	var notes []Note
	if err := db.Where("deck_id = ?", deckID).Find(&notes).Error; err != nil {
		t.Fatalf("load notes: %v", err)
	}
	noteByID := make(map[uint64]Note, len(notes))
	for i := range notes {
		noteByID[notes[i].ID] = notes[i]
	}
	var cards []Card
	if err := db.Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", deckID).Find(&cards).Error; err != nil {
		t.Fatalf("load cards: %v", err)
	}
	if len(cards) == 0 {
		t.Fatal("the deck holds no live card")
	}
	for i := range cards {
		card := cards[i]
		note, ok := noteByID[card.NoteID]
		if !ok {
			t.Fatalf("card %d belongs to an unknown note", card.ID)
		}
		if note.NoteTypeID == nil {
			t.Fatalf("note %d has no note type", note.ID)
		}
		cfg, ok := cfgByID[*note.NoteTypeID]
		if !ok {
			t.Fatalf("note %d uses an unknown note type %d", note.ID, *note.NoteTypeID)
		}
		data := notetype.RenderData{
			Fields:   NoteFieldValues(note.FieldsJSON),
			Tags:     TagsOrEmpty(note.TagsJSON),
			TypeName: nameByID[*note.NoteTypeID],
			CardID:   strconv.FormatUint(card.ID, 10),
		}
		var tmpl notetype.Template
		if cfg.Kind == notetype.KindCloze {
			ordinal, err := strconv.Atoi(strings.TrimPrefix(card.Template, "cloze:"))
			if err != nil || !strings.HasPrefix(card.Template, "cloze:") {
				t.Fatalf("cloze card %d has template %q", card.ID, card.Template)
			}
			if len(cfg.Templates) == 0 {
				t.Fatalf("cloze note type %q has no template", nameByID[*note.NoteTypeID])
			}
			tmpl = cfg.Templates[0]
			data.ClozeOrdinal = ordinal
		} else {
			found := false
			for _, candidate := range cfg.Templates {
				if candidate.ID == card.Template {
					tmpl, found = candidate, true
					break
				}
			}
			if !found {
				t.Fatalf("card %d keeps template %q, which note type %q does not declare", card.ID, card.Template, nameByID[*note.NoteTypeID])
			}
		}
		data.CardName = tmpl.Name
		if _, err := notetype.Render(cfg, tmpl, notetype.SideFront, data); err != nil {
			t.Errorf("card %d does not render: %v", card.ID, err)
		}
	}
}
