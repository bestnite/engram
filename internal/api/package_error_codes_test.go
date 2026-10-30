package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the REST-side acceptance for "each deck package error code surfaces on its own":
// mapPackageError no longer folds package_bad_format into a blanket invalid_request but passes through
// store.PackageError.Code one code at a time. Each case also asserts the HTTP status is unchanged (this task only changes the code in the body).
//
// Before the fix a bad zip got code=invalid_request (case red); after it gets code=package_bad_format (green).

// decodePackageErrorCode extracts the stable code and message from the error envelope.
func decodePackageErrorCode(t *testing.T, raw []byte) (code, message string) {
	t.Helper()
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, raw)
	}
	return resp.Error.Code, resp.Error.Message
}

// packageZip builds zip deck package bytes from the given entry name -> content pairs.
func packageZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
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

// presetJSON is the minimal valid preset.json (the same one minimalPackageZip uses).
const presetJSON = `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`

// TestImportPackageSurfacesBadFormat asserts a bad zip (not an archive) yields its own stable code
// package_bad_format, the status is still 400, and the message is no longer the blanket "request invalid".
func TestImportPackageSurfacesBadFormat(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_bad_format", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	body, ct := multipartBody(t, "bad.edeck", []byte("this is not a zip"))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-format import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageBadFormat {
		t.Errorf("bad-format import code = %q, want %q", code, store.CodePackageBadFormat)
	}
	// With per-code surfacing the message is the English text of the i18n key error.package_bad_format, no longer falling back to invalid_request.
	if msg == errorMessages[CodeInvalidRequest] {
		t.Errorf("bad-format import message fell back to invalid_request: %q", msg)
	}
}

// TestImportPackageSurfacesTooLarge asserts a package over the body cap yields the stable code package_too_large,
// with the same status as before the fix (413).
func TestImportPackageSurfacesTooLarge(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_too_large", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_MAX_BYTES", "")
	if err := store.PutSetting(context.Background(), env.db, "media_max_bytes", "256", nil, time.Now().UTC()); err != nil {
		t.Fatalf("seed media_max_bytes: %v", err)
	}

	body, ct := multipartBody(t, "big.edeck", bytes.Repeat([]byte("A"), 4096))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	code, _ := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageTooLarge {
		t.Errorf("oversized import code = %q, want %q", code, store.CodePackageTooLarge)
	}
}

// TestImportPackageSurfacesUnknownKind asserts a note naming an undeclared note type reference yields the stable
// code package_unknown_kind, the status is still 400, and the failing notes' indexes are listed one by one.
func TestImportPackageSurfacesUnknownKind(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_unknown_kind", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	raw := packageZip(t, map[string]string{
		"manifest.json":   `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":0}}`,
		"note_types.json": packageTestNoteTypesJSON,
		"notes.json":      `[{"note_type":"nt-9","fields":{"front":"q","back":"a"}}]`,
		"cards.json":      `[]`,
		"preset.json":     presetJSON,
	})
	body, ct := multipartBody(t, "unknown.edeck", raw)
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-kind import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageUnknownKind {
		t.Errorf("unknown-kind import code = %q, want %q", code, store.CodePackageUnknownKind)
	}
	// entries are still listed one by one: the message names the failing notes' indexes.
	if !bytes.Contains([]byte(msg), []byte("notes[0]")) {
		t.Errorf("unknown-kind message lost the per-note entries: %q", msg)
	}
}

// TestImportPackageSurfacesObsoleteFormat asserts a format-1 package yields package_format_obsolete and the
// message tells the user to import it with an older release and export it again.
func TestImportPackageSurfacesObsoleteFormat(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_obsolete_format", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	raw := packageZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":0}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"q","back":"a"}}]`,
		"cards.json":    `[]`,
		"preset.json":   presetJSON,
	})
	body, ct := multipartBody(t, "format1.edeck", raw)
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("obsolete-format import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageFormatObsolete {
		t.Errorf("obsolete-format import code = %q, want %q", code, store.CodePackageFormatObsolete)
	}
	if !bytes.Contains([]byte(msg), []byte("older engram release")) {
		t.Errorf("obsolete-format message = %q, want guidance to use an older release", msg)
	}
}

// TestImportPackageSurfacesUnsafeMedia was removed with the media allowlist: import now accepts every class.
