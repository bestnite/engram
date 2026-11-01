package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestImportEntriesReportBadConflictOptionAsInvalidRequest is the acceptance for M19-107: a malformed
// on_conflict is a mistake in the request, not in the archive, so every import entry answers the same
// 400 invalid_request rather than package_bad_format.
func TestImportEntriesReportBadConflictOptionAsInvalidRequest(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_option_code", store.RoleUser)
	key := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	packageBytes := minimalPackageZip(t)

	check := func(t *testing.T, name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %s)", name, rec.Code, rec.Body.String())
		}
		if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeInvalidRequest {
			t.Errorf("%s code = %q, want %q (body %s)", name, code, CodeInvalidRequest, rec.Body.String())
		}
	}

	t.Run("multipart", func(t *testing.T) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		if err := w.WriteField("on_conflict", "bogus"); err != nil {
			t.Fatalf("write on_conflict field: %v", err)
		}
		fw, err := w.CreateFormFile("file", "deck.apkg")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(packageBytes); err != nil {
			t.Fatalf("write form file: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		check(t, "multipart import", postImport(t, env, key.Plaintext, &buf, w.FormDataContentType()))
	})

	t.Run("direct_link", func(t *testing.T) {
		env.api.fetcher = &stubFetcher{body: packageBytes}
		rec := postImportURL(t, env, key.Plaintext,
			`{"url":"https://example.com/deck.apkg","on_conflict":"bogus"}`)
		check(t, "direct-link import", rec)
	})

	t.Run("upload_ticket_issuance", func(t *testing.T) {
		check(t, "upload ticket issuance", createUpload(t, env, key.Plaintext, `{"on_conflict":"bogus"}`))
	})
}
