package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestImportPackageAuthorizesBeforeReadingArchive is the acceptance for the multipart ordering
// contract: the handler reads the form fields and authorizes the target before it consumes the
// archive part, so a refused target never spools the package to a temporary file.
//
// An existing deck the caller holds no role on is refused with 403 forbidden (the deck exists but
// the caller has no grant; a public id that does not exist would be 404 not_found). The positive
// control reuses the same oversized part with target=new_deck: once the target is authorized the
// store cap still applies, so the response is 413 package_too_large and its temporary file is gone.
func TestImportPackageAuthorizesBeforeReadingArchive(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "auth_order_owner", store.RoleUser)
	caller := seedUser(t, env.db, "auth_order_caller", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, caller.ID, []string{store.ScopeWrite}, nil)

	t.Setenv(store.EnvPackageMaxBytes, "")
	if err := store.PutSetting(t.Context(), env.db, store.SettingKeyPackageMaxBytes, "256", nil, time.Now().UTC()); err != nil {
		t.Fatalf("seed package_max_bytes: %v", err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// postOversized sends the fields before the archive part, which is what a client must do under
	// the fields-before-archive contract; the archive is far over the 256-byte cap.
	postOversized := func(t *testing.T, target string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		if err := w.WriteField("target", target); err != nil {
			t.Fatalf("write target: %v", err)
		}
		f, err := w.CreateFormFile("file", "big.apkg")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := f.Write(bytes.Repeat([]byte("A"), 4096)); err != nil {
			t.Fatalf("write form file: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		return postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
	}
	assertNoTempFile := func(t *testing.T) {
		t.Helper()
		entries, err := os.ReadDir(tmp)
		if err != nil {
			t.Fatalf("read TMPDIR: %v", err)
		}
		for _, e := range entries {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}

	t.Run("foreign target is refused before spooling", func(t *testing.T) {
		rec := postOversized(t, fmt.Sprintf("into_deck:%s", deck.PublicID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("foreign target import = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeForbidden {
			t.Errorf("error code = %q, want %q", code, CodeForbidden)
		}
		assertNoTempFile(t)
	})

	t.Run("unknown target is refused before spooling", func(t *testing.T) {
		rec := postOversized(t, "into_deck:does-not-exist")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown target import = %d, want 404 (body %s)", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeNotFound {
			t.Errorf("error code = %q, want %q", code, CodeNotFound)
		}
		assertNoTempFile(t)
	})

	t.Run("oversized archive for an authorized target still hits the cap", func(t *testing.T) {
		rec := postOversized(t, "new_deck")
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("oversized import = %d, want 413 (body %s)", rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != store.CodePackageTooLarge {
			t.Errorf("error code = %q, want %q", code, store.CodePackageTooLarge)
		}
		assertNoTempFile(t)
	})
}

// TestImportPackageRejectsFilePartBeforeFields locks the other half of the ordering contract: a
// request that puts the archive before any field part is refused instead of falling back to the
// new_deck defaults the client never asked for.
func TestImportPackageRejectsFilePartBeforeFields(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "file_first", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, err := w.CreateFormFile("file", "deck.apkg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := f.Write(minimalPackageZip(t)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := postImport(t, env, key.Plaintext, &body, w.FormDataContentType())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("file-first import = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeInvalidRequest {
		t.Errorf("error code = %q, want %q", code, CodeInvalidRequest)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("form fields must precede the file part")) {
		t.Errorf("response does not name the ordering contract: %s", rec.Body.String())
	}
}
