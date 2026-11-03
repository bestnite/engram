package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the media picker's backend entry: POST /api/v1/decks/:id/media/names.
//
// It registers the deck-scoped name of a medium that is already stored, so the field editor can insert an existing library
// item without uploading it again. The rules under test are the endpoint's contract: editor role plus CSRF, a readable
// medium, the response name derived from the stored mime, idempotency, and the requirement that an imported alias for the
// same bytes survives as an additional alias rather than being renamed or repointed.

// mediaNameRegistrationPath is the endpoint path for one deck's name registration.
func mediaNameRegistrationPath(deckPublicID string) string {
	return "/api/v1/decks/" + deckPublicID + "/media/names"
}

// postMediaName sends one registration request through the real router, with the session CSRF token in its header.
func postMediaName(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, deckPublicID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode registration body: %v", err)
	}
	return jsonRequest(t, srv, http.MethodPost, mediaNameRegistrationPath(deckPublicID), string(payload), cookies, csrf)
}

// postMediaNameRaw sends a raw request body, for the malformed-body cases.
func postMediaNameRaw(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, deckPublicID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return jsonRequest(t, srv, http.MethodPost, mediaNameRegistrationPath(deckPublicID), body, cookies, csrf)
}

// mediaNameResp is the success body: the canonical deck-scoped name of the medium.
type mediaNameResp struct {
	Name string `json:"name"`
}

// decodeMediaNameResp decodes a successful registration response.
func decodeMediaNameResp(t *testing.T, rec *httptest.ResponseRecorder) mediaNameResp {
	t.Helper()
	var body mediaNameResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode registration response: %v (body %s)", err, rec.Body.String())
	}
	return body
}

// mediaErrorEnvelope is the uniform error shape the web media handlers write.
type mediaErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// mediaErrorCode decodes the stable code of a failed registration response.
func mediaErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope mediaErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, rec.Body.String())
	}
	return envelope.Error.Code
}

// countMediaNameRows counts the deck's media_names rows that point at one medium.
func countMediaNameRows(t *testing.T, db *gorm.DB, deckID uint64, sha string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.MediaName{}).Where("deck_id = ? AND sha256 = ?", deckID, sha).Count(&n).Error; err != nil {
		t.Fatalf("count media names: %v", err)
	}
	return n
}

// mediaNameRow loads one (deck, name) row; nil means the row does not exist.
func mediaNameRow(t *testing.T, db *gorm.DB, deckID uint64, name string) *store.MediaName {
	t.Helper()
	var row store.MediaName
	err := db.Where("deck_id = ? AND name = ?", deckID, name).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		t.Fatalf("load media name %q: %v", name, err)
	}
	return &row
}

// TestDeckMediaNameRegisterRegistersTheCanonicalName is the editor success case: a stored image the caller can read is
// named for the deck as "<sha256>.png", and the frame media route serves that name so the field can show it before a save.
func TestDeckMediaNameRegisterRegistersTheCanonicalName(t *testing.T) {
	f := newFrameMediaFixture(t)
	raw := append(pngBody(), []byte("picker-canonical")...)
	media := saveFrameMedia(t, f.db, f.srv, raw, f.owner.ID)

	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": media.Sha256})
	if rec.Code != http.StatusOK {
		t.Fatalf("registration status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeMediaNameResp(t, rec)
	want := media.Sha256 + ".png"
	if body.Name != want {
		t.Fatalf("name = %q, want %q", body.Name, want)
	}
	if row := mediaNameRow(t, f.db, f.deck.ID, want); row == nil || row.Sha256 != media.Sha256 {
		t.Fatalf("media_names row for %q = %+v, want sha %q", want, row, media.Sha256)
	}
	// The editing frame resolves the inserted name against the deck's media base, so the freshly registered name must be
	// served through it before any note references the medium.
	served := f.get(t, "/fm/"+f.token(t, f.owner.ID, f.deck.ID)+"/"+want)
	if served.Code != http.StatusOK || served.Body.String() != string(raw) {
		t.Fatalf("GET /fm/<token>/%s = %d, want 200 with the stored bytes", want, served.Code)
	}
}

// TestDeckMediaNameRegisterUsesTheStoredMime asserts the extension comes from the stored mime, never from the request.
func TestDeckMediaNameRegisterUsesTheStoredMime(t *testing.T) {
	f := newFrameMediaFixture(t)
	raw := append([]byte("ID3"), []byte("picker-audio")...)
	audio, err := store.NewMediaStore(f.db).SaveBytes(context.Background(), f.srv.media.Root(), "audio/mpeg", raw, store.Ptr(f.owner.ID))
	if err != nil {
		t.Fatalf("SaveBytes(audio) error = %v", err)
	}
	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": audio.Sha256})
	if rec.Code != http.StatusOK {
		t.Fatalf("registration status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if body := decodeMediaNameResp(t, rec); body.Name != audio.Sha256+".mp3" {
		t.Fatalf("name = %q, want %q", body.Name, audio.Sha256+".mp3")
	}
}

// TestDeckMediaNameRegisterIsIdempotent asserts a repeated request returns the same name and writes no second row.
func TestDeckMediaNameRegisterIsIdempotent(t *testing.T) {
	f := newFrameMediaFixture(t)
	media := saveFrameMedia(t, f.db, f.srv, append(pngBody(), []byte("picker-repeat")...), f.owner.ID)
	first := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": media.Sha256})
	if first.Code != http.StatusOK {
		t.Fatalf("first registration = %d, want 200 (body %s)", first.Code, first.Body.String())
	}
	second := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": media.Sha256})
	if second.Code != http.StatusOK {
		t.Fatalf("second registration = %d, want 200 (body %s)", second.Code, second.Body.String())
	}
	if a, b := decodeMediaNameResp(t, first).Name, decodeMediaNameResp(t, second).Name; a != b {
		t.Fatalf("repeated registration returned %q then %q, want the same name", a, b)
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256); n != 1 {
		t.Fatalf("media_names rows after two registrations = %d, want 1", n)
	}
}

// TestDeckMediaNameRegisterKeepsAnImportedAlias asserts the canonical name is an additional alias: a deck that already
// holds some imported name for these bytes keeps that name pointing at the same sha, and gains "<sha256>.png" beside it.
func TestDeckMediaNameRegisterKeepsAnImportedAlias(t *testing.T) {
	f := newFrameMediaFixture(t)
	media := saveFrameMedia(t, f.db, f.srv, append(pngBody(), []byte("picker-imported-alias")...), f.owner.ID)
	const alias = "imported-picture.png"
	if err := store.NewMediaNameStore(f.db).Upsert(context.Background(), f.deck.ID, alias, media.Sha256); err != nil {
		t.Fatalf("seed imported alias: %v", err)
	}

	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": media.Sha256})
	if rec.Code != http.StatusOK {
		t.Fatalf("registration status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	canonical := media.Sha256 + ".png"
	if body := decodeMediaNameResp(t, rec); body.Name != canonical {
		t.Fatalf("name = %q, want the canonical name %q", body.Name, canonical)
	}
	// The alias was neither renamed nor repointed; the canonical name is a second row for the same bytes.
	kept := mediaNameRow(t, f.db, f.deck.ID, alias)
	if kept == nil || kept.Sha256 != media.Sha256 {
		t.Fatalf("imported alias after registration = %+v, want it kept with sha %q", kept, media.Sha256)
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256); n != 2 {
		t.Fatalf("media_names rows for the medium = %d, want the alias plus the canonical name", n)
	}
}

// TestDeckMediaNameRegisterRefusesAReader asserts the role check runs before anything is written: a reader of the deck
// cannot register a name, even for a medium they can read through the same deck.
func TestDeckMediaNameRegisterRefusesAReader(t *testing.T) {
	f := newFrameMediaFixture(t)
	media := saveFrameMedia(t, f.db, f.srv, append(pngBody(), []byte("picker-reader")...), f.owner.ID)
	// The note makes the medium readable for the reader; the refusal below is the role check, not the readability check.
	if _, err := store.NewNoteStore(f.db).Create(context.Background(), &store.Note{
		DeckID: f.deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, f.db, f.deck.ID, "basic")), CreatedBy: store.Ptr(f.owner.ID),
	}, map[string]any{"front": `<img src="` + media.Sha256 + `.png">`, "back": "back"}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	// The note write path already gives the referenced medium its canonical name in this deck, so the refused request is
	// asserted to leave that row set unchanged rather than to leave it empty.
	before := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256)
	rec := postMediaName(t, f.srv, f.readerCookies, f.readerCSRF, f.deck.PublicID, map[string]string{"sha256": media.Sha256})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader registration = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if after := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256); after != before {
		t.Fatalf("media_names rows after a refused reader request = %d, want the %d rows from the note write", after, before)
	}
}

// TestDeckMediaNameRegisterRefusesUnreadableMedia asserts an editor who cannot read the bytes gets media_not_readable and
// no row: the same refusal the note write path would give the reference later.
func TestDeckMediaNameRegisterRefusesUnreadableMedia(t *testing.T) {
	f := newFrameMediaFixture(t)
	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": f.foreign.Sha256})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unreadable registration = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if code := mediaErrorCode(t, rec); code != store.CodeMediaNotReadable {
		t.Fatalf("error code = %q, want %q", code, store.CodeMediaNotReadable)
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, f.foreign.Sha256); n != 0 {
		t.Fatalf("media_names rows after an unreadable request = %d, want 0", n)
	}
}

// TestDeckMediaNameRegisterMissingMedia asserts an unknown sha is 404 media_not_found: a medium no row holds cannot be named.
func TestDeckMediaNameRegisterMissingMedia(t *testing.T) {
	f := newFrameMediaFixture(t)
	missing := strings.Repeat("0", 64)
	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": missing})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing-media registration = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if code := mediaErrorCode(t, rec); code != codeMediaNotFound {
		t.Fatalf("error code = %q, want %q", code, codeMediaNotFound)
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, missing); n != 0 {
		t.Fatalf("media_names rows for a missing medium = %d, want 0", n)
	}
}

// TestDeckMediaNameRegisterRejectsBadRequests covers the request-shape refusals: every case is 400 and writes no row.
func TestDeckMediaNameRegisterRejectsBadRequests(t *testing.T) {
	f := newFrameMediaFixture(t)
	media := saveFrameMedia(t, f.db, f.srv, append(pngBody(), []byte("picker-bad-requests")...), f.owner.ID)
	shas := []string{
		"",
		"abc",
		strings.Repeat("A", 64),   // uppercase hex is not the sha256 shape
		strings.Repeat("g", 64),   // non-hex character
		media.Sha256 + "0",        // 65 characters
		strings.Repeat("0", 63),   // 63 characters
		" /media/" + media.Sha256, // path-like input
	}
	for _, sha := range shas {
		t.Run("sha="+sha, func(t *testing.T) {
			rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": sha})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			if code := mediaErrorCode(t, rec); code != "invalid_request" {
				t.Fatalf("error code = %q, want invalid_request", code)
			}
		})
	}
	rawBodies := []string{
		``,
		`{`,
		`[]`,
		`{"sha256": 42}`,
		`{"name": "x.png"}`,
	}
	for _, body := range rawBodies {
		t.Run("body="+body, func(t *testing.T) {
			rec := postMediaNameRaw(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256); n != 0 {
		t.Fatalf("media_names rows after refused requests = %d, want 0", n)
	}
}

// TestDeckMediaNameRegisterNeedsSessionAndCSRF covers the transport refusals: no session, no CSRF token and an unknown deck.
func TestDeckMediaNameRegisterNeedsSessionAndCSRF(t *testing.T) {
	f := newFrameMediaFixture(t)
	media := saveFrameMedia(t, f.db, f.srv, append(pngBody(), []byte("picker-transport")...), f.owner.ID)
	body := map[string]string{"sha256": media.Sha256}

	anonymous := postMediaName(t, f.srv, nil, "", f.deck.PublicID, body)
	if anonymous.Code != http.StatusForbidden || !strings.Contains(anonymous.Body.String(), "csrf_no_session") {
		t.Fatalf("anonymous registration = %d %s, want 403 csrf_no_session", anonymous.Code, anonymous.Body.String())
	}
	noCSRF := postMediaName(t, f.srv, f.ownerCookies, "", f.deck.PublicID, body)
	if noCSRF.Code != http.StatusForbidden || !strings.Contains(noCSRF.Body.String(), "csrf_failed") {
		t.Fatalf("registration without CSRF = %d %s, want 403 csrf_failed", noCSRF.Code, noCSRF.Body.String())
	}
	unknownDeck := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, "00000000-0000-0000-0000-000000000000", body)
	if unknownDeck.Code != http.StatusNotFound {
		t.Fatalf("registration into an unknown deck = %d, want 404 (body %s)", unknownDeck.Code, unknownDeck.Body.String())
	}
	if n := countMediaNameRows(t, f.db, f.deck.ID, media.Sha256); n != 0 {
		t.Fatalf("media_names rows after refused transports = %d, want 0", n)
	}
}

// TestDeckMediaNameRegisterCrossDeckPick is the picker's cross-deck case: the medium comes from a deck the caller can only
// read (not from their own uploads), the name is registered in the deck being edited, and a note saved with the insertion
// still resolves the medium afterwards.
func TestDeckMediaNameRegisterCrossDeckPick(t *testing.T) {
	f := newFrameMediaFixture(t)
	ctx := context.Background()
	// f.foreign was supplied by the fixture's third account, which is the uploader and therefore can read it. A note in a
	// deck owned by that account, shared with the editor as a reader, is what makes the medium readable to the editor.
	if f.foreign.CreatedBy == nil {
		t.Fatal("fixture foreign medium has no uploader")
	}
	strangerID := *f.foreign.CreatedBy
	strangerDeck := seedDeck(t, f.db, strangerID, "Picker stranger deck")
	if err := store.NewGrantStore(f.db).Grant(ctx, strangerDeck.ID, f.owner.ID, store.RoleReader, store.Ptr(strangerID)); err != nil {
		t.Fatalf("grant editor read access: %v", err)
	}
	if _, err := store.NewNoteStore(f.db).Create(ctx, &store.Note{
		DeckID: strangerDeck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, f.db, strangerDeck.ID, "basic")), CreatedBy: store.Ptr(strangerID),
	}, map[string]any{"front": `<img src="` + f.foreign.Sha256 + `.png">`, "back": "back"}); err != nil {
		t.Fatalf("create note in the stranger deck: %v", err)
	}

	// The note write already named the medium in the source deck; the registration must only touch the edited deck, so
	// the source row set is compared before and after.
	sourceBefore := countMediaNameRows(t, f.db, strangerDeck.ID, f.foreign.Sha256)
	rec := postMediaName(t, f.srv, f.ownerCookies, f.ownerCSRF, f.deck.PublicID, map[string]string{"sha256": f.foreign.Sha256})
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-deck registration = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	name := decodeMediaNameResp(t, rec).Name
	if name != f.foreign.Sha256+".png" {
		t.Fatalf("name = %q, want %q", name, f.foreign.Sha256+".png")
	}
	if row := mediaNameRow(t, f.db, f.deck.ID, name); row == nil || row.Sha256 != f.foreign.Sha256 {
		t.Fatalf("canonical name in the edited deck = %+v, want sha %q", row, f.foreign.Sha256)
	}
	if sourceAfter := countMediaNameRows(t, f.db, strangerDeck.ID, f.foreign.Sha256); sourceAfter != sourceBefore {
		t.Fatalf("the source deck's name rows changed from %d to %d, want the registration scoped to the edited deck", sourceBefore, sourceAfter)
	}

	// Saving the insertion stores the same name, and the editing preview still resolves it through the frame base.
	fields := map[string]any{"front": `<img src="` + name + `">`, "back": "back"}
	if _, err := store.NewNoteStore(f.db).Create(ctx, &store.Note{
		DeckID: f.deck.ID, NoteTypeID: store.Ptr(stockNoteTypeID(t, f.db, f.deck.ID, "basic")), CreatedBy: store.Ptr(f.owner.ID),
	}, fields); err != nil {
		t.Fatalf("save the note with the picked insertion: %v", err)
	}
	preview := f.preview(t, "basic", fields, true)
	if len(preview.Cards) == 0 || !strings.Contains(preview.Cards[0].Front.HTML, `src="`+name+`"`) {
		t.Fatalf("frame preview did not resolve the picked name %q: %+v", name, preview.Cards)
	}
}
