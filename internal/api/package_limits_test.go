package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for the request body cap on the REST import entries. The cap comes from the same source as web uploads (the admin-configured
// media_max_bytes), and must take effect before multipart parsing, with a stable 413 when exceeded.

// multipartBody builds a multipart request body holding only a file field, plus its Content-Type.
func multipartBody(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// postImport sends a multipart import request with a bearer key.
func postImport(t *testing.T, env *testEnv, key string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/import", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	env.router().ServeHTTP(rec, req)
	return rec
}

// packageTestNoteTypesJSON is the note_types.json of the hand-built test packages: one normal type.
const packageTestNoteTypesJSON = `[{"ref":"nt-1","name":"Basic","config":{"kind":"normal","fields":[{"id":"front","name":"front"},{"id":"back","name":"back"}],"templates":[{"id":"t-forward","name":"Forward","qfmt":"{{front}}","afmt":"{{front}}<hr>{{back}}"}]}}]`

// minimalPackageZip builds a minimal valid .edeck (one basic note, no media, no progress).
func minimalPackageZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"manifest.json":   `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":1}}`,
		"note_types.json": packageTestNoteTypesJSON,
		"notes.json":      `[{"note_type":"nt-1","fields":{"front":"q","back":"a"}}]`,
		"cards.json":      `[]`,
		"preset.json":     `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestImportPackageRejectsOversizedBody is the core negative case: with media_max_bytes set to 256 bytes,
// a 4 KiB multipart body must be rejected before parsing (413, code package_too_large).
//
// Before the fix the handler called FormFile first, gin read the whole 4 KiB part into memory before handling, and the result was 400
// (not a zip) instead of 413 - which is exactly why this case goes red before the fix.
func TestImportPackageRejectsOversizedBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_oversize", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	// Turn off the environment override so the value from the settings table is read.
	t.Setenv("MEDIA_MAX_BYTES", "")
	if err := env.db.Create(&store.Setting{Key: "media_max_bytes", Value: "256", UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("seed media_max_bytes: %v", err)
	}
	// TMPDIR points at an empty directory: assert an over-limit request leaves no multipart temp file.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	body, ct := multipartBody(t, "big.edeck", bytes.Repeat([]byte("A"), 4096))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if resp.Error.Code != store.CodePackageTooLarge {
		t.Errorf("error code = %q, want %q", resp.Error.Code, store.CodePackageTooLarge)
	}
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			t.Errorf("temp upload file left behind after rejection: %s", e.Name())
		}
	}
}

// TestImportPackageAcceptsBodyWithinLimit is the positive control: a valid package within the default cap still imports successfully.
func TestImportPackageAcceptsBodyWithinLimit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_within", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_MAX_BYTES", "")

	body, ct := multipartBody(t, "deck.edeck", minimalPackageZip(t))
	rec := postImport(t, env, k.Plaintext, body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid import = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 1 {
		t.Errorf("decks = %d, want 1 after a successful import", decks)
	}
}
