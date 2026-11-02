package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for Anki archives: POST /api/v1/decks/import sniffs the
// archive instead of the file name, imports the package, and a corrupt archive is a 400 with the
// package_bad_format code rather than a 500.

func ankiFixtureBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../notetype/testdata/anki/stock.anki21b.apkg")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

func TestImportReservedPrefixNoticeLocalized(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "prefix-import", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	cfg := notetype.Config{Kind: notetype.KindNormal, Fields: []notetype.Field{{ID: "front", Name: "Front"}, {ID: "legacy", Name: "engram_notes"}}, Templates: []notetype.Template{{ID: "t", Name: "Card", QFormat: "{{Front}}"}}}
	nt := store.NoteType{DeckID: deck.ID, Name: "Imported prefix"}
	if err := store.NewNoteTypeStore(env.db).Create(context.Background(), &nt, cfg); err != nil {
		t.Fatal(err)
	}
	note := store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(nt.ID)}
	if _, err := store.NewNoteStore(env.db).Create(context.Background(), &note, map[string]any{"Front": "front", "engram_notes": "ordinary"}); err != nil {
		t.Fatal(err)
	}
	raw, err := env.api.ExportAnkiPackage(context.Background(), user.ID, deck.ID, store.PackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	translator, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tag  language.Tag
		want string
	}{{language.English, "ordinary fields"}, {language.SimplifiedChinese, "普通字段导入"}} {
		ctx := i18n.WithLocalizer(context.Background(), translator.Localizer(tc.tag))
		report, err := env.api.ImportDeckPackage(ctx, user, nil, bytes.NewReader(raw), store.PackageImportOptions{Target: store.PackageTargetNewDeck})
		if err != nil {
			t.Fatal(err)
		}
		if len(report.ReservedPrefixFields) != 1 || report.ReservedPrefixFields[0] != "engram_notes" || !strings.Contains(report.ReservedPrefixNotice, tc.want) {
			t.Fatalf("%v: %+v", tc.tag, report)
		}
	}
}

func TestImportPackageAcceptsAnki(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_apkg", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	body, contentType := multipartBody(t, "stock.apkg", ankiFixtureBytes(t))
	rec := postImport(t, env, key.Plaintext, body, contentType)
	if rec.Code != http.StatusOK {
		t.Fatalf("import .apkg status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var report store.PackageImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.NotesCreated != 8 || report.DeckPublicID == "" {
		t.Fatalf("report = %+v, want eight notes in a new deck", report)
	}
	if report.MatchRule != "anki_guid" {
		t.Fatalf("match_rule = %q, want anki_guid", report.MatchRule)
	}
	if report.MediaMissing != 0 {
		t.Fatalf("media_missing = %d, want 0 for the stock fixture", report.MediaMissing)
	}
	// The stock fixture is a modern Anki package, which carries no engram key, so the report
	// must expose the lost machine grading over REST as a positive count.
	if report.AnswerSpecsMissing == 0 {
		t.Fatalf("answer_specs_missing = 0, want the modern fixture's note types counted: %+v", report)
	}
}

func TestImportPackageCorruptAnkiIsBadFormat(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_apkg_bad", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string][]byte{
		"meta":             {0xff},
		"collection.anki2": []byte("not a collection"),
	}
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	body, contentType := multipartBody(t, "broken.apkg", buf.Bytes())
	rec := postImport(t, env, key.Plaintext, body, contentType)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("corrupt .apkg status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if envelope.Error.Code != store.CodePackageBadFormat {
		t.Fatalf("code = %q, want %q", envelope.Error.Code, store.CodePackageBadFormat)
	}
	// The rejected package leaves no deck behind.
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Fatalf("rejected package left %d decks", decks)
	}
}
