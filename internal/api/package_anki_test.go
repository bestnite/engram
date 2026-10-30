package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for .apkg on the same REST entry as .edeck: POST /api/v1/decks/import sniffs the
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
