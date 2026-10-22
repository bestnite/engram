package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for upload ticket import (issued by POST /api/v1/decks/import-uploads, uploaded via PUT .../:token).
//
// The positive cases prove bytes can complete an import without a bearer key and that the audit is recorded against the key used for issuance; the negative cases cover every
// way a ticket can go bad (replay, expiry, key revocation, account disabled, forgery) plus authorization and parameter validation at issuance.

// createUpload issues a ticket with a bearer key and returns the raw response.
func createUpload(t *testing.T, env *testEnv, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/import-uploads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	env.router().ServeHTTP(rec, req)
	return rec
}

// issueUpload issues a ticket, asserts success and returns the parsed result.
func issueUpload(t *testing.T, env *testEnv, key, body string) ImportUpload {
	t.Helper()
	rec := createUpload(t, env, key, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create import upload = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var up ImportUpload
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode import upload: %v (%s)", err, rec.Body.String())
	}
	return up
}

// putUpload PUTs body to the ticket URL (its path part) with no credentials at all.
func putUpload(t *testing.T, env *testEnv, uploadURL string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	env.router().ServeHTTP(rec, req)
	return rec
}

// minimalPackageDocument is the JSON document form of minimalPackageZip (the output form of export_deck).
func minimalPackageDocument() []byte {
	return []byte(`{
  "manifest.json": {"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"from-json"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":1}},
  "notes.json": [{"kind":"basic","fields":{"front":"q","back":"a"}}],
  "cards.json": [],
  "preset.json": {"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}
}`)
}

func TestImportUploadImportsWithoutBearer(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{name: "edeck archive", body: minimalPackageZip(t)},
		{name: "json document", body: minimalPackageDocument()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "up_ok_"+safeName(tc.name), store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

			up := issueUpload(t, env, k.Plaintext, `{"target":"new_deck"}`)
			if up.Method != http.MethodPut || !strings.HasPrefix(up.UploadURL, importUploadPathPrefix) {
				t.Fatalf("upload = %+v, want PUT to %s<token>", up, importUploadPathPrefix)
			}
			if !up.ExpiresAt.Equal(env.now.Add(importUploadTTL)) || up.MaxBytes <= 0 {
				t.Fatalf("upload expiry/limit = %v/%d, want %v and a positive limit", up.ExpiresAt, up.MaxBytes, env.now.Add(importUploadTTL))
			}

			rec := putUpload(t, env, up.UploadURL, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var report store.PackageImportReport
			if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if report.NotesCreated != 1 || report.DeckPublicID == "" {
				t.Fatalf("report = %+v, want one note in a new deck", report)
			}
			// The audit is recorded against the issuer and the key used for issuance: the upload request itself carries no key, so ownership must not be lost.
			var logs []store.AuditLog
			if err := env.db.Where("action = ?", "deck.package_import").Find(&logs).Error; err != nil {
				t.Fatalf("load audit: %v", err)
			}
			if len(logs) != 1 || logs[0].UserID == nil || *logs[0].UserID != user.ID || logs[0].APIKeyID == nil || *logs[0].APIKeyID != k.Key.ID {
				t.Fatalf("audit = %+v, want one deck.package_import by user %d with key %d", logs, user.ID, k.Key.ID)
			}
		})
	}
}

func TestImportUploadAbsoluteURLFromBaseURL(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	env.api.baseURL = "https://example.com"
	user := seedUser(t, env.db, "up_base", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	up := issueUpload(t, env, k.Plaintext, `{}`)
	if !strings.HasPrefix(up.UploadURL, "https://example.com"+importUploadPathPrefix) {
		t.Fatalf("upload_url = %q, want it rooted at the configured base URL", up.UploadURL)
	}
}

// TestImportUploadRejectsInvalidTicket covers every way a ticket goes bad: all get the same 404 code,
// and no import happens.
func TestImportUploadRejectsInvalidTicket(t *testing.T) {
	cases := []struct {
		name string
		// spoil invalidates the ticket after issuance and before upload; it returns the URL to PUT to.
		spoil func(t *testing.T, env *testEnv, user *store.User, k *store.CreatedAPIKey, uploadURL string) string
	}{
		{name: "replayed", spoil: func(t *testing.T, env *testEnv, _ *store.User, _ *store.CreatedAPIKey, uploadURL string) string {
			if rec := putUpload(t, env, uploadURL, minimalPackageZip(t)); rec.Code != http.StatusOK {
				t.Fatalf("first upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			return uploadURL
		}},
		{name: "expired", spoil: func(_ *testing.T, env *testEnv, _ *store.User, _ *store.CreatedAPIKey, uploadURL string) string {
			later := env.now.Add(importUploadTTL)
			env.api.now = func() time.Time { return later }
			return uploadURL
		}},
		{name: "key revoked", spoil: func(t *testing.T, env *testEnv, user *store.User, k *store.CreatedAPIKey, uploadURL string) string {
			if err := env.keys.Revoke(context.Background(), user.ID, k.Key.ID, env.now); err != nil {
				t.Fatalf("revoke key: %v", err)
			}
			return uploadURL
		}},
		{name: "user disabled", spoil: func(t *testing.T, env *testEnv, user *store.User, _ *store.CreatedAPIKey, uploadURL string) string {
			if err := env.db.Model(&store.User{}).Where("id = ?", user.ID).Update("status", store.StatusDisabled).Error; err != nil {
				t.Fatalf("disable user: %v", err)
			}
			return uploadURL
		}},
		{name: "forged token", spoil: func(_ *testing.T, _ *testEnv, _ *store.User, _ *store.CreatedAPIKey, _ string) string {
			return importUploadPathPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "up_bad_"+safeName(tc.name), store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
			up := issueUpload(t, env, k.Plaintext, `{"target":"new_deck"}`)

			target := tc.spoil(t, env, user, k, up.UploadURL)
			var before int64
			env.db.Model(&store.Deck{}).Count(&before)
			rec := putUpload(t, env, target, minimalPackageZip(t))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("upload = %d, want 404 (body %s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeImportUploadInvalid {
				t.Fatalf("code = %q, want %q", code, CodeImportUploadInvalid)
			}
			var after int64
			env.db.Model(&store.Deck{}).Count(&after)
			if after != before {
				t.Fatalf("deck count %d -> %d, want no import", before, after)
			}
		})
	}
}

// TestCreateImportUploadRejects covers rejections at issuance: missing scope, no rights on the target deck, insufficient role, invalid parameters.
// A rejected issuance must leave no ticket behind.
func TestCreateImportUploadRejects(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "up_owner", store.RoleUser)
	other := seedUser(t, env.db, "up_other", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	readKey := seedKey(t, env.keys, other.ID, []string{store.ScopeRead}, nil)
	writeKey := seedKey(t, env.keys, other.ID, []string{store.ScopeWrite}, nil)

	cases := []struct {
		name       string
		key        string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"read scope", readKey.Plaintext, `{"target":"new_deck"}`, http.StatusForbidden, CodeScopeRequired},
		{"into a deck without access", writeKey.Plaintext, fmt.Sprintf(`{"target":"into_deck:%s"}`, deck.PublicID), http.StatusForbidden, CodeForbidden},
		{"unknown deck", writeKey.Plaintext, `{"target":"into_deck:nope"}`, http.StatusNotFound, CodeNotFound},
		{"bad on_conflict", writeKey.Plaintext, `{"on_conflict":"merge"}`, http.StatusBadRequest, CodeInvalidRequest},
		{"malformed body", writeKey.Plaintext, `{`, http.StatusBadRequest, CodeInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := createUpload(t, env, tc.key, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("create = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != tc.wantCode {
				t.Fatalf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
	var n int64
	env.db.Model(&store.ActionToken{}).Where("purpose = ?", importUploadPurpose).Count(&n)
	if n != 0 {
		t.Fatalf("rejected issuance left %d tickets, want 0", n)
	}
}

// TestImportUploadRechecksRoleAtUpload: the role is revoked after issuance, so the upload is rejected per the current role.
func TestImportUploadRechecksRoleAtUpload(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "up_recheck_owner", store.RoleUser)
	editor := seedUser(t, env.db, "up_recheck_editor", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	grant := store.DeckGrant{DeckID: deck.ID, UserID: editor.ID, Role: store.RoleEditor, CreatedAt: env.now}
	if err := env.db.Create(&grant).Error; err != nil {
		t.Fatalf("grant editor: %v", err)
	}
	k := seedKey(t, env.keys, editor.ID, []string{store.ScopeWrite}, nil)
	up := issueUpload(t, env, k.Plaintext, fmt.Sprintf(`{"target":"into_deck:%s"}`, deck.PublicID))

	if err := env.db.Where("deck_id = ? AND user_id = ?", deck.ID, editor.ID).Delete(&store.DeckGrant{}).Error; err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	rec := putUpload(t, env, up.UploadURL, minimalPackageZip(t))
	if rec.Code == http.StatusOK {
		t.Fatalf("upload after losing the role = 200, want a rejection (body %s)", rec.Body.String())
	}
	var notes int64
	env.db.Model(&store.Note{}).Where("deck_id = ?", deck.ID).Count(&notes)
	if notes != 0 {
		t.Fatalf("deck has %d notes, want 0", notes)
	}
}

func TestImportUploadRejectsOversizedBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "up_big", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_MAX_BYTES", "256")
	up := issueUpload(t, env, k.Plaintext, `{}`)
	if up.MaxBytes != 256 {
		t.Fatalf("max_bytes = %d, want 256", up.MaxBytes)
	}

	cases := []struct {
		name string
		body []byte
	}{
		{name: "archive", body: append(minimalPackageZip(t), bytes.Repeat([]byte{0}, 4096)...)},
		{name: "json document", body: append([]byte(`{"notes.json":"`), append(bytes.Repeat([]byte("A"), 4096), []byte(`"}`)...)...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := issueUpload(t, env, k.Plaintext, `{}`)
			rec := putUpload(t, env, up.UploadURL, tc.body)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized upload = %d, want 413 (body %s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != store.CodePackageTooLarge {
				t.Fatalf("code = %q, want %q", code, store.CodePackageTooLarge)
			}
		})
	}
}

func TestImportUploadMalformedPackage(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "up_malformed", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	for _, body := range [][]byte{[]byte(`{"manifest.json":`), []byte("not a zip")} {
		up := issueUpload(t, env, k.Plaintext, `{}`)
		rec := putUpload(t, env, up.UploadURL, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed upload %q = %d, want 400 (body %s)", body, rec.Code, rec.Body.String())
		}
	}
}
