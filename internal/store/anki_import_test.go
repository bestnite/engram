package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

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
