package web

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers: the media side of the editor — uploading an image and inserting it into a card field.
// The acceptance line is "the uploaded image renders in the preview, and is still there after a page reload"; permission and validation negative cases come alongside.

// uploadMediaTo submits a multipart to the given upload endpoint, to distinguish the /media and /decks/:id/media entries.
func uploadMediaTo(t *testing.T, srv *Server, cookie []*http.Cookie, target, csrf, filename, declared string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if csrf != "" {
		_ = w.WriteField("csrf_token", csrf)
	}
	part, err := w.CreatePart(textProto(filename, declared))
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range cookie {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// mediaUploadResp is the metadata returned by a successful upload.
type mediaUploadResp struct {
	Sha256 string `json:"sha256"`
	Mime   string `json:"mime"`
	URL    string `json:"url"`
}

// mediaShaOK validates the sha256 shape in an upload/URL: exactly 64 lowercase hex characters.
func mediaShaOK(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// TestEditorMediaUploadInsertsAndSurvivesReload is the main acceptance: upload an image through the in-deck upload
// entry the editor uses → insert it into a field as a Markdown reference → the preview renders an <img> → save → after a reload the field value still
// holds the reference and the preview still has the <img> (and it is not base64-inlined).
func TestEditorMediaUploadInsertsAndSurvivesReload(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Media deck")
	note := seedBasic(t, db, deck.ID, "front", "back")
	deckPath := "/decks/" + deck.PublicID

	// 1) Upload the image through the in-deck upload entry.
	up := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrf, "pic.png", "image/png", pngBody())
	if up.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) || saved.URL != "/media/"+saved.Sha256 {
		t.Fatalf("upload returned url %q sha %q, want /media/<sha256>", saved.URL, saved.Sha256)
	}

	// 2) The proxy can fetch the image back (via the existing /media/<sha256>, not a separate storage stack).
	img := getWithCookies(t, srv, saved.URL, cookies)
	if img.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, img.Code)
	}
	if ct := img.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("GET %s Content-Type = %q, want image/png", saved.URL, ct)
	}

	// 3) Insert a media reference into the front field. A field value is HTML, and media is referenced by its deck-scoped
	// file name (the upload URL's sha with the stored extension).
	name := strings.TrimPrefix(saved.URL, "/media/")
	ref := `<img src="` + name + `">`
	prev := postJSONWithCSRF(t, srv, "/api/v1/decks/"+deck.PublicID+"/notes/preview", map[string]any{
		"kind":   "basic",
		"fields": map[string]any{"front": "see " + ref, "back": "back"},
	}, cookies, csrf)
	if prev.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", prev.Code, prev.Body.String())
	}
	var prevBody struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(prev.Body.Bytes(), &prevBody); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(prevBody.Cards) == 0 || !strings.Contains(prevBody.Cards[0].FrontHTML, `<img src="`+saved.URL+`">`) {
		t.Fatalf("preview did not resolve the uploaded image to its absolute URL: %s", snippet(prev.Body.String()))
	}

	// 4) Save the reference (SPA card updates go through PATCH /api/v1/notes/:id).
	saveBody, err := json.Marshal(map[string]any{
		"kind":   "basic",
		"fields": map[string]any{"front": "see " + ref, "back": "back"},
	})
	if err != nil {
		t.Fatalf("encode save body: %v", err)
	}
	save := jsonRequest(t, srv, http.MethodPatch, "/api/v1/notes/"+note.PublicID, string(saveBody), cookies, csrf)
	if save.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200 (body %s)", save.Code, save.Body.String())
	}

	// 5) Preview again: the field value still holds the reference and the preview still has the <img> (and it is not base64-inlined).
	reload := postJSONWithCSRF(t, srv, "/api/v1/decks/"+deck.PublicID+"/notes/preview", map[string]any{
		"kind":   "basic",
		"fields": map[string]any{"front": "see " + ref, "back": "back"},
	}, cookies, csrf)
	if reload.Code != http.StatusOK {
		t.Fatalf("reload preview status = %d, want 200", reload.Code)
	}
	var reloadBody struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(reload.Body.Bytes(), &reloadBody); err != nil {
		t.Fatalf("decode reload preview: %v", err)
	}
	if len(reloadBody.Cards) == 0 {
		t.Fatalf("reload preview returned no cards: %s", snippet(reload.Body.String()))
	}
	body := reloadBody.Cards[0].FrontHTML
	if !strings.Contains(body, saved.URL) {
		t.Errorf("reloaded preview lost the /media reference: %s", snippet(body))
	}
	if !strings.Contains(body, `<img src="`+saved.URL+`">`) {
		t.Errorf("reloaded preview lost the <img>: %s", snippet(body))
	}
	if strings.Contains(body, "data:image") {
		t.Errorf("media was inlined as base64 instead of referenced")
	}

	// The storage layer confirms the field holds a reference, not inline data.
	stored, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("load note: %v", err)
	}
	fields, err := store.ParseFields(stored.FieldsJSON)
	if err != nil {
		t.Fatalf("parse fields: %v", err)
	}
	if front, _ := fields["front"].(string); !strings.Contains(front, name) {
		t.Errorf("stored front %q does not contain the media name %q", front, name)
	}
}

// TestEditorMediaUploadRejectsOversizedAndEmpty covers the negative cases of upload validation:
// above the admin-configured limit -> 413 + media_too_large; an empty file -> 422 + media_magic_mismatch.
func TestEditorMediaUploadRejectsOversizedAndEmpty(t *testing.T) {
	t.Setenv("MEDIA_MAX_BYTES", "1024")
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Limits deck")
	deckPath := "/decks/" + deck.PublicID

	// Over the limit: a valid PNG header + padding, more than 1 KiB.
	big := append(pngBody(), bytes.Repeat([]byte("x"), 2048)...)
	over := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrf, "big.png", "image/png", big)
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413 (body %s)", over.Code, over.Body.String())
	}
	if !strings.Contains(over.Body.String(), "media_too_large") {
		t.Errorf("oversized upload did not return the stable code: %s", over.Body.String())
	}

	// Empty file: there is no content to classify or store.
	bad := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrfOrEmpty(csrf), "empty.png", "image/png", nil)
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty upload status = %d, want 422 (body %s)", bad.Code, bad.Body.String())
	}
	if !strings.Contains(bad.Body.String(), "media_magic_mismatch") {
		t.Errorf("empty upload did not return the stable code: %s", bad.Body.String())
	}
}

// TestEditorMediaUploadRequiresEditorRoleAndCSRF covers the negative cases for permission and CSRF:
// a reader cannot push media into someone else's deck; an upload without CSRF is refused.
func TestEditorMediaUploadRequiresEditorRoleAndCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Role deck")
	deckPath := "/decks/" + deck.PublicID

	// Grant the second user the reader role.
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "media-reader")
	grantRole(t, srv, deck.ID, user2ID, store.RoleReader, ownerCookies, ownerCSRF)

	// A reader's upload is refused with 403.
	reader := uploadMediaTo(t, srv, u2Cookies, deckPath+"/media", u2CSRF, "pic.png", "image/png", pngBody())
	if reader.Code != http.StatusForbidden {
		t.Fatalf("reader upload status = %d, want 403 (body %s)", reader.Code, reader.Body.String())
	}

	// An upload without CSRF is refused with 403.
	noCSRF := uploadMediaTo(t, srv, ownerCookies, deckPath+"/media", "", "pic.png", "image/png", pngBody())
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("upload without CSRF status = %d, want 403 (body %s)", noCSRF.Code, noCSRF.Body.String())
	}
}

// csrfOrEmpty makes call sites read more plainly (the wrong-magic case still carries a valid CSRF; only the CSRF cases leave it empty).
func csrfOrEmpty(s string) string { return s }
