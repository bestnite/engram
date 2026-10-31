package store

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// seedAnkiExportDeck builds the source deck the export cases use: two note types (one
// with an answer spec), notes with tags and an external guid, a subdeck path, one media
// file, one card state and one review.
type ankiExportFixture struct {
	owner        uint64
	deckID       uint64
	basicTypeID  uint64
	answerTypeID uint64
	basicNoteID  uint64
	answerNoteID uint64
	basicCardID  uint64
	answerCardID uint64
	mediaRoot    string
	mediaName    string
}

func seedAnkiExportDeck(t *testing.T, db *gorm.DB, owner uint64) ankiExportFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	weights := make([]float64, 21)
	for i := range weights {
		weights[i] = 0.1 * float64(i+1)
	}
	rawWeights, err := json.Marshal(weights)
	if err != nil {
		t.Fatalf("encode weights: %v", err)
	}
	preset := Preset{
		OwnerUserID: owner, Name: "Anki preset", DesiredRetention: 0.87,
		LearningSteps: "1m,10m", RelearningSteps: "20m", MaximumIntervalDays: 999,
		EnableFuzz: Ptr(false), WeightsJSON: Ptr(string(rawWeights)), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := Deck{OwnerUserID: owner, Name: "Source deck", Description: "example deck", PresetID: preset.ID, CreatedAt: now}
	if err := NewDeckStore(db).Create(ctx, &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}

	basicTypeID := stockNoteTypeID(t, db, deck.ID, "basic")
	answerType := NoteType{DeckID: deck.ID, Name: "Answer type"}
	if err := NewNoteTypeStore(db).Create(ctx, &answerType, noteTypeAnswerConfig()); err != nil {
		t.Fatalf("create answer note type: %v", err)
	}

	mediaRoot := t.TempDir()
	mediaBytes := append([]byte("\x89PNG\r\n\x1a\n"), []byte("example-payload")...)
	media, err := NewMediaStore(db).SaveBytes(ctx, mediaRoot, "image/png", mediaBytes, Ptr(owner))
	if err != nil {
		t.Fatalf("save media: %v", err)
	}
	const mediaName = "pic.png"
	if err := NewMediaNameStore(db).Upsert(ctx, deck.ID, mediaName, media.Sha256); err != nil {
		t.Fatalf("register media name: %v", err)
	}

	basic := Note{
		DeckID: deck.ID, NoteTypeID: Ptr(basicTypeID), TagsJSON: TagsJSON([]string{"geo"}),
		ExternalRef: Ptr("anki:stock-guid-1"), Source: Ptr("manual"), CreatedBy: Ptr(owner),
	}
	basicCards, err := NewNoteStore(db).Create(ctx, &basic, map[string]any{
		"front": "capital of France?", "back": `<img src="pic.png"> Paris`,
	})
	if err != nil {
		t.Fatalf("create basic note: %v", err)
	}
	answer := Note{
		DeckID: deck.ID, NoteTypeID: Ptr(answerType.ID), TagsJSON: TagsJSON([]string{"math"}),
		Source: Ptr("manual"), CreatedBy: Ptr(owner),
	}
	answerCards, err := NewNoteStore(db).Create(ctx, &answer, map[string]any{
		"f-front": "2 + 2?", "f-back": "4",
	})
	if err != nil {
		t.Fatalf("create answer note: %v", err)
	}
	if err := db.Model(&Card{}).Where("id = ?", answerCards[0].ID).
		Update("deck_path", "Lesson 1::Verbs").Error; err != nil {
		t.Fatalf("set subdeck path: %v", err)
	}

	// Anki stores review due dates as whole days relative to collection creation.
	dueAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if err := db.Create(&CardState{
		CardID: basicCards[0].ID, UserID: owner, State: "review", DueAt: &dueAt,
		StepIndex: 0, Stability: Ptr(4.5), Difficulty: Ptr(6.2), Reps: 3, Lapses: 1,
		ScheduledDays: 5, ElapsedDays: 2, LastReviewAt: &now, Version: 4,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	reviewedAt := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	if err := db.Create(&Review{
		CardID: basicCards[0].ID, UserID: owner, Rating: 3, GradeSource: "self",
		ReviewedAt: reviewedAt, ReviewDay: "2026-10-01", ElapsedMS: Ptr(1200),
		StateBefore: 2, IntervalDays: Ptr(5.0),
		Stability: Ptr(4.5), Difficulty: Ptr(6.2),
	}).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}

	return ankiExportFixture{
		owner: owner, deckID: deck.ID, basicTypeID: basicTypeID, answerTypeID: answerType.ID,
		basicNoteID: basic.ID, answerNoteID: answer.ID,
		basicCardID: basicCards[0].ID, answerCardID: answerCards[0].ID,
		mediaRoot: mediaRoot, mediaName: mediaName,
	}
}

// TestExportAnkiPackageRoundTrip checks content and progress through both package paths.
func TestExportAnkiPackageRoundTrip(t *testing.T) {
	sources := packageDatabases(t)
	destinations := packageDatabases(t)
	for driver, db := range sources {
		dst := destinations[driver]
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_export_owner")[0]
			fixture := seedAnkiExportDeck(t, db, owner)

			opts := PackageOptions{
				IncludeProgress: true, IncludeReviews: true, IncludeMedia: true,
				IncludeDeckConfigs: Ptr(true), IncludeWeights: true, MediaRoot: fixture.mediaRoot,
			}
			first, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, fixture.deckID, opts)
			if err != nil {
				t.Fatalf("ExportAnkiPackage: %v", err)
			}
			second, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, fixture.deckID, opts)
			if err != nil {
				t.Fatalf("second ExportAnkiPackage: %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("two exports of one deck differ: %d vs %d bytes", len(first), len(second))
			}

			assertAnkiExportScheduling(t, first)

			importer := seedUsers(t, dst, "anki_export_importer")[0]
			dstRoot := t.TempDir()
			report, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(first), PackageImportOptions{
				Target: PackageTargetNewDeck, MediaRoot: dstRoot, NewDeckName: "Imported deck", IncludeProgress: true,
			})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 2 || report.CardsCreated != 2 {
				t.Fatalf("report notes=%d cards=%d, want 2/2", report.NotesCreated, report.CardsCreated)
			}

			assertAnkiExportNoteTypes(t, db, dst, fixture, report.DeckID)
			assertAnkiExportNotesAndCards(t, dst, report.DeckID)
			assertAnkiExportMedia(t, dst, dstRoot, report.DeckID)
			var source CardState
			if err := db.Where("card_id = ? AND user_id = ?", fixture.basicCardID, owner).First(&source).Error; err != nil {
				t.Fatal(err)
			}
			var notes []Note
			if err := dst.Where("deck_id = ?", report.DeckID).Find(&notes).Error; err != nil {
				t.Fatal(err)
			}
			var note Note
			for _, n := range notes {
				if n.ExternalRef != nil && *n.ExternalRef == "anki:stock-guid-1" {
					note = n
				}
			}
			var card Card
			if err := dst.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
				t.Fatal(err)
			}
			var got CardState
			if err := dst.Where("card_id = ? AND user_id = ?", card.ID, importer).First(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got.State != source.State || got.DueAt == nil || !got.DueAt.Equal(*source.DueAt) || got.Stability == nil || *got.Stability != *source.Stability || got.Difficulty == nil || *got.Difficulty != *source.Difficulty || got.Reps != source.Reps || got.Lapses != source.Lapses {
				t.Fatalf("state: got %+v want %+v", got, source)
			}
			if got.LastReviewAt == nil || source.LastReviewAt == nil || !got.LastReviewAt.Equal(*source.LastReviewAt) {
				t.Fatalf("last review: got %v want %v", got.LastReviewAt, source.LastReviewAt)
			}
			var sourceReview, review Review
			if err := db.Where("user_id = ?", owner).First(&sourceReview).Error; err != nil {
				t.Fatal(err)
			}
			if err := dst.Where("user_id = ?", importer).First(&review).Error; err != nil {
				t.Fatal(err)
			}
			if review.Rating != sourceReview.Rating || !review.ReviewedAt.Equal(sourceReview.ReviewedAt) || review.ElapsedMS == nil || *review.ElapsedMS != *sourceReview.ElapsedMS || review.StateBefore != sourceReview.StateBefore {
				t.Fatalf("review: got %+v want %+v", review, sourceReview)
			}
			if !reflect.DeepEqual(review.IntervalDays, sourceReview.IntervalDays) || !reflect.DeepEqual(review.DurationDays, sourceReview.DurationDays) {
				t.Fatalf("review intervals: got %+v want %+v", review, sourceReview)
			}
			again, err := NewDeckStore(dst).ImportPackage(ctx, importer, bytes.NewReader(first), PackageImportOptions{Target: "into_deck:" + strconv.FormatUint(report.DeckID, 10), MediaRoot: dstRoot, IncludeProgress: true})
			if err != nil {
				t.Fatal(err)
			}
			var stateCount, reviewCount int64
			dst.Model(&CardState{}).Where("user_id = ?", importer).Count(&stateCount)
			dst.Model(&Review{}).Where("user_id = ?", importer).Count(&reviewCount)
			if stateCount != 2 || reviewCount != 1 || again.ProgressApplied != 0 || again.ReviewsApplied != 0 {
				t.Fatalf("repeat: states=%d reviews=%d report=%+v", stateCount, reviewCount, again)
			}
			offUser := seedUsers(t, dst, "anki_progress_off")[0]
			off, err := NewDeckStore(dst).ImportPackage(ctx, offUser, bytes.NewReader(first), PackageImportOptions{Target: PackageTargetNewDeck, MediaRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			dst.Model(&CardState{}).Where("user_id = ?", offUser).Count(&stateCount)
			dst.Model(&Review{}).Where("user_id = ?", offUser).Count(&reviewCount)
			if stateCount != 0 || reviewCount != 0 || off.StatesDiscarded == 0 || off.ReviewsDiscarded == 0 {
				t.Fatalf("off: states=%d reviews=%d report=%+v", stateCount, reviewCount, off)
			}
		})
	}
}

// TestExportAnkiPackageSwitchesAreIndependent proves reviews can travel without card
// states and that the deck configuration is on by default while progress is off.
func TestExportAnkiPackageSwitchesAreIndependent(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "anki_switches_owner")[0]
			fixture := seedAnkiExportDeck(t, db, owner)

			raw, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, fixture.deckID, PackageOptions{
				IncludeReviews: true, IncludeMedia: true, MediaRoot: fixture.mediaRoot,
			})
			if err != nil {
				t.Fatalf("ExportAnkiPackage: %v", err)
			}
			col := readAnkiCollection(t, raw)
			var dconfJSON string
			if err := col.QueryRow(`SELECT dconf FROM col`).Scan(&dconfJSON); err != nil {
				t.Fatalf("read dconf: %v", err)
			}
			var dconf map[string]map[string]any
			if err := json.Unmarshal([]byte(dconfJSON), &dconf); err != nil {
				t.Fatalf("decode dconf: %v", err)
			}
			if len(dconf) != 1 {
				t.Fatalf("dconf has %d entries, want 1", len(dconf))
			}
			if _, hasOne := dconf["1"]; hasOne {
				t.Fatalf("dconf wrote a config with id 1")
			}
			var reviews int
			if err := col.QueryRow(`SELECT count(*) FROM revlog`).Scan(&reviews); err != nil {
				t.Fatalf("count revlog: %v", err)
			}
			if reviews != 1 {
				t.Fatalf("revlog rows = %d, want 1", reviews)
			}
			var reviewState int
			if err := col.QueryRow(`SELECT type FROM cards WHERE id = (SELECT cid FROM revlog)`).Scan(&reviewState); err != nil {
				t.Fatalf("read reviewed card: %v", err)
			}
			if reviewState != 0 {
				t.Fatalf("card type = %d, want 0 (content-only when progress is off)", reviewState)
			}
		})
	}
}

// assertAnkiExportScheduling opens the exported collection and checks the card state and
// review mapping.
func assertAnkiExportScheduling(t *testing.T, raw []byte) {
	t.Helper()
	col := readAnkiCollection(t, raw)
	var cardType, queue, ivl, reps, lapses int
	if err := col.QueryRow(`SELECT type, queue, ivl, reps, lapses FROM cards WHERE id = 1`).
		Scan(&cardType, &queue, &ivl, &reps, &lapses); err != nil {
		t.Fatalf("read card state: %v", err)
	}
	if cardType != 2 || queue != 2 || ivl != 5 || reps != 3 || lapses != 1 {
		t.Errorf("card scheduling = type %d queue %d ivl %d reps %d lapses %d, want 2/2/5/3/1",
			cardType, queue, ivl, reps, lapses)
	}
	var cid, ease, reviewIvl, lastIvl int
	if err := col.QueryRow(`SELECT cid, ease, ivl, lastIvl FROM revlog`).Scan(&cid, &ease, &reviewIvl, &lastIvl); err != nil {
		t.Fatalf("read revlog: %v", err)
	}
	var resolved int
	if err := col.QueryRow(`SELECT count(*) FROM cards WHERE id = ?`, cid).Scan(&resolved); err != nil {
		t.Fatalf("resolve revlog cid: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("revlog cid %d does not name a card in the archive", cid)
	}
	if ease != 3 || reviewIvl != 5 {
		t.Errorf("revlog = ease %d ivl %d, want 3/5", ease, reviewIvl)
	}
	var dconfJSON string
	if err := col.QueryRow(`SELECT dconf FROM col`).Scan(&dconfJSON); err != nil {
		t.Fatalf("read dconf: %v", err)
	}
	var dconf map[string]map[string]any
	if err := json.Unmarshal([]byte(dconfJSON), &dconf); err != nil {
		t.Fatalf("decode dconf: %v", err)
	}
	config, ok := dconf["2"]
	if !ok {
		t.Fatalf("dconf has no entry 2: %s", dconfJSON)
	}
	if got := len(config["fsrsParams6"].([]any)); got != 21 {
		t.Errorf("fsrsParams6 has %d values, want 21", got)
	}
	if got := len(config["fsrsWeights"].([]any)); got != 0 {
		t.Errorf("fsrsWeights has %d values, want 0", got)
	}
	var ver int
	if err := col.QueryRow(`SELECT ver FROM col`).Scan(&ver); err != nil {
		t.Fatalf("read col.ver: %v", err)
	}
	if ver != 11 {
		t.Errorf("col.ver = %d, want 11", ver)
	}
}

func assertAnkiExportNoteTypes(t *testing.T, src, dst *gorm.DB, fixture ankiExportFixture, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	imported, err := NewNoteTypeStore(dst).ListByDeck(ctx, deckID)
	if err != nil {
		t.Fatalf("list imported note types: %v", err)
	}
	byName := make(map[string]notetype.Config, len(imported))
	for i := range imported {
		cfg, err := imported[i].Config()
		if err != nil {
			t.Fatalf("decode imported note type: %v", err)
		}
		byName[imported[i].Name] = cfg
	}
	for _, original := range []struct {
		id   uint64
		name string
	}{{fixture.basicTypeID, "Basic"}, {fixture.answerTypeID, "Answer type"}} {
		row := NoteType{}
		if err := src.First(&row, "id = ?", original.id).Error; err != nil {
			t.Fatalf("load original note type: %v", err)
		}
		want, err := row.Config()
		if err != nil {
			t.Fatalf("decode original note type: %v", err)
		}
		got, ok := byName[original.name]
		if !ok {
			t.Fatalf("imported note type %q missing", original.name)
		}
		if len(got.Fields) != len(want.Fields) || len(got.Templates) != len(want.Templates) {
			t.Fatalf("%s shape = %d fields/%d templates, want %d/%d",
				original.name, len(got.Fields), len(got.Templates), len(want.Fields), len(want.Templates))
		}
		for i := range want.Fields {
			if got.Fields[i].Name != want.Fields[i].Name {
				t.Errorf("%s field %d = %q, want %q", original.name, i, got.Fields[i].Name, want.Fields[i].Name)
			}
		}
		for i := range want.Templates {
			if got.Templates[i].Name != want.Templates[i].Name ||
				got.Templates[i].QFormat != want.Templates[i].QFormat ||
				got.Templates[i].AFormat != want.Templates[i].AFormat {
				t.Errorf("%s template %d differs", original.name, i)
			}
		}
		if got.CSS != want.CSS {
			t.Errorf("%s CSS = %q, want %q", original.name, got.CSS, want.CSS)
		}
		wantAnswer := want.Templates[0].Answer
		gotAnswer := got.Templates[0].Answer
		if (wantAnswer == nil) != (gotAnswer == nil) {
			t.Fatalf("%s answer spec presence = %v, want %v", original.name, gotAnswer != nil, wantAnswer != nil)
		}
		if wantAnswer != nil {
			if gotAnswer.Control != wantAnswer.Control {
				t.Errorf("%s answer control = %q, want %q", original.name, gotAnswer.Control, wantAnswer.Control)
			}
			// Binding values are field ids in the imported type; resolve them to names and
			// compare with the original binding's field name.
			fieldName := map[string]string{}
			for _, field := range got.Fields {
				fieldName[field.ID] = field.Name
			}
			wantName := ""
			for _, field := range want.Fields {
				if field.ID == wantAnswer.Bindings["value"] {
					wantName = field.Name
				}
			}
			if fieldName[gotAnswer.Bindings["value"]] != wantName {
				t.Errorf("%s answer binding = %q, want field %q",
					original.name, fieldName[gotAnswer.Bindings["value"]], wantName)
			}
		}
	}
}

func assertAnkiExportNotesAndCards(t *testing.T, db *gorm.DB, deckID uint64) {
	t.Helper()
	var notes []Note
	if err := db.Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		t.Fatalf("list imported notes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("imported notes = %d, want 2", len(notes))
	}
	byRef := make(map[string]Note, len(notes))
	for _, note := range notes {
		if note.ExternalRef != nil {
			byRef[*note.ExternalRef] = note
		}
	}
	basic, ok := byRef["anki:stock-guid-1"]
	if !ok {
		t.Fatalf("imported notes have no stock guid: %+v", notes)
	}
	basicValues := ankiFieldValuesByName(t, db, &basic)
	if got := basicValues["front"]; got != "capital of France?" {
		t.Errorf("basic front = %q", got)
	}
	if got := basicValues["back"]; got != `<img src="pic.png"> Paris` {
		t.Errorf("basic back = %q", got)
	}
	tags, err := ParseTags(basic.TagsJSON)
	if err != nil || len(tags) != 1 || tags[0] != "geo" {
		t.Errorf("basic tags = %v (%v), want [geo]", tags, err)
	}

	answerNote := Note{}
	for _, note := range notes {
		if note.ID != basic.ID {
			answerNote = note
		}
	}
	answerValues := ankiFieldValuesByName(t, db, &answerNote)
	if answerValues["Front"] != "2 + 2?" || answerValues["Back"] != "4" {
		t.Errorf("answer fields = %v, want Front=2 + 2? Back=4", answerValues)
	}
	answerTags, err := ParseTags(answerNote.TagsJSON)
	if err != nil || len(answerTags) != 1 || answerTags[0] != "math" {
		t.Errorf("answer tags = %v (%v), want [math]", answerTags, err)
	}

	var cards []Card
	if err := db.Where("note_id IN ?", []uint64{basic.ID, answerNote.ID}).Order("note_id ASC").Find(&cards).Error; err != nil {
		t.Fatalf("list imported cards: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("imported cards = %d, want 2", len(cards))
	}
	for _, card := range cards {
		switch card.NoteID {
		case basic.ID:
			if card.DeckPath != nil {
				t.Errorf("flat card deck_path = %v, want nil", *card.DeckPath)
			}
		case answerNote.ID:
			if card.DeckPath == nil || *card.DeckPath != "Lesson 1::Verbs" {
				t.Errorf("subdeck card deck_path = %v, want Lesson 1::Verbs", card.DeckPath)
			}
		}
	}
}

func assertAnkiExportMedia(t *testing.T, db *gorm.DB, dstRoot string, deckID uint64) {
	t.Helper()
	ctx := context.Background()
	names, err := NewMediaNameStore(db).ListByDeck(ctx, deckID)
	if err != nil {
		t.Fatalf("list imported media names: %v", err)
	}
	if len(names) != 1 || names[0].Name != "pic.png" {
		t.Fatalf("imported media names = %+v, want pic.png", names)
	}
	media, err := NewMediaStore(db).BySha256(ctx, names[0].Sha256)
	if err != nil || media == nil {
		t.Fatalf("load imported media metadata: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dstRoot, filepath.FromSlash(media.RelPath)))
	if err != nil {
		t.Fatalf("read imported media bytes: %v", err)
	}
	if !bytes.Equal(raw, append([]byte("\x89PNG\r\n\x1a\n"), []byte("example-payload")...)) {
		t.Fatalf("imported media bytes differ")
	}
}

// ankiFieldValuesByName decodes a note's stored fields keyed by field name, because a
// package import assigns its own field ids.
func ankiFieldValuesByName(t *testing.T, db *gorm.DB, note *Note) map[string]string {
	t.Helper()
	if note.NoteTypeID == nil {
		t.Fatalf("note %d has no note type", note.ID)
	}
	row := NoteType{}
	if err := db.First(&row, "id = ?", *note.NoteTypeID).Error; err != nil {
		t.Fatalf("load note type %d: %v", *note.NoteTypeID, err)
	}
	cfg, err := row.Config()
	if err != nil {
		t.Fatalf("decode note type: %v", err)
	}
	values, err := ParseFieldValues(note.FieldsJSON)
	if err != nil {
		t.Fatalf("decode note fields: %v", err)
	}
	out := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		out[field.Name] = values[field.ID]
	}
	return out
}

// readAnkiCollection extracts collection.anki21 from the archive and opens it with the
// pure-Go SQLite driver.
func readAnkiCollection(t *testing.T, raw []byte) *sql.DB {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	var payload []byte
	for _, f := range zr.File {
		if f.Name != "collection.anki21" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open collection entry: %v", err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			rc.Close()
			t.Fatalf("read collection entry: %v", err)
		}
		rc.Close()
		payload = buf.Bytes()
	}
	if payload == nil {
		t.Fatalf("archive has no collection.anki21")
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("collection-%d.sqlite", time.Now().UnixNano()))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write collection: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open collection: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
