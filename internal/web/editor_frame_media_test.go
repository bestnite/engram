package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the media path of the rich-text editing frame. The frame is sandboxed and carries no session cookie,
// so it can show a field's media only through the frame media route: the editor asks for the deck's media base, and an
// upload through the editor's entry registers the deck-scoped name that the frame then resolves.

// editorMediaBaseResp is the frame media base endpoint's response.
type editorMediaBaseResp struct {
	MediaBase string `json:"media_base"`
}

func TestEditorFrameMediaBaseAccess(t *testing.T) {
	f := newFrameMediaFixture(t)
	cases := []struct {
		name    string
		deckID  string
		cookies []*http.Cookie
		want    int
	}{
		{name: "editor gets the base", deckID: f.deck.PublicID, cookies: f.ownerCookies, want: http.StatusOK},
		{name: "reader is refused", deckID: f.deck.PublicID, cookies: f.readerCookies, want: http.StatusForbidden},
		{name: "reader of no grant is refused", deckID: f.otherDeck.PublicID, cookies: f.readerCookies, want: http.StatusForbidden},
		{name: "no session is refused", deckID: f.deck.PublicID, cookies: nil, want: http.StatusUnauthorized},
		{name: "unknown deck", deckID: "00000000-0000-0000-0000-000000000000", cookies: f.ownerCookies, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWithCookies(t, f.srv, "/api/v1/decks/"+tc.deckID+"/frame-media-base", tc.cookies)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			var body editorMediaBaseResp
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
			}
			// The base must open the deck's media for this user: the fixture's stored name is served through it.
			token := frameMediaTokenFromBase(t, body.MediaBase)
			if got := f.get(t, "/fm/"+token+"/"+f.readable.Sha256+".png"); got.Code != http.StatusOK {
				t.Errorf("GET through the editor base = %d, want 200", got.Code)
			}
		})
	}
}

func TestEditorFrameMediaBaseWithoutSecretsIsEmpty(t *testing.T) {
	srv, _, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, srv.db, ownerID, "No secrets editor")
	rec := getWithCookies(t, srv, "/api/v1/decks/"+deck.PublicID+"/frame-media-base", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body editorMediaBaseResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MediaBase != "" {
		t.Errorf("media_base = %q without secrets, want empty", body.MediaBase)
	}
}

// TestEditorUploadNameIsServedByTheFrame asserts that an editor upload can be shown in the editing frame before any
// note references it: the response names the file, and the frame media route serves that name for the deck.
func TestEditorUploadNameIsServedByTheFrame(t *testing.T) {
	f := newFrameMediaFixture(t)
	raw := append(pngBody(), []byte("editor-frame-upload")...)
	rec := uploadMediaTo(t, f.srv, f.ownerCookies, "/decks/"+f.deck.PublicID+"/media", f.ownerCSRF, "map.png", "image/png", raw)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var up struct {
		Sha256 string `json:"sha256"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	// The name is the form the note write path gives an editor reference, so a later save stores the same text.
	if up.Name != up.Sha256+".png" {
		t.Fatalf("name = %q, want %q", up.Name, up.Sha256+".png")
	}
	got := f.get(t, "/fm/"+f.token(t, f.owner.ID, f.deck.ID)+"/"+up.Name)
	if got.Code != http.StatusOK || got.Body.String() != string(raw) {
		t.Fatalf("GET /fm/<token>/<name> = %d, want 200 with the uploaded bytes", got.Code)
	}
	// The name belongs to the deck it was uploaded to and to no other.
	if other := f.get(t, "/fm/"+f.token(t, f.owner.ID, f.otherDeck.ID)+"/"+up.Name); other.Code != http.StatusNotFound {
		t.Errorf("GET through another deck = %d, want 404", other.Code)
	}
}

// TestGenericUploadNamesNoDeck asserts that the deck-less upload entry registers no name: it has no deck to scope one to.
func TestGenericUploadNamesNoDeck(t *testing.T) {
	f := newFrameMediaFixture(t)
	rec := uploadMediaTo(t, f.srv, f.ownerCookies, "/media", f.ownerCSRF, "plain.png", "image/png", append(pngBody(), []byte("generic")...))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var up map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &up); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if _, ok := up["name"]; ok {
		t.Errorf("generic upload response carries a name: %v", up)
	}
	var count int64
	if err := f.db.Model(&store.MediaName{}).Where("sha256 = ?", up["sha256"]).Count(&count).Error; err != nil {
		t.Fatalf("count media names: %v", err)
	}
	if count != 0 {
		t.Errorf("media_names rows for a generic upload = %d, want 0", count)
	}
}

// TestEditorUploadByReaderRegistersNoName asserts the refused upload leaves no name behind.
func TestEditorUploadByReaderRegistersNoName(t *testing.T) {
	f := newFrameMediaFixture(t)
	rec := uploadMediaTo(t, f.srv, f.readerCookies, "/decks/"+f.deck.PublicID+"/media", f.readerCSRF, "x.png", "image/png", append(pngBody(), []byte("reader")...))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader upload status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var count int64
	if err := f.db.Model(&store.MediaName{}).Where("deck_id = ? AND name LIKE ?", f.deck.ID, "%.png").
		Where("sha256 <> ?", f.readable.Sha256).Count(&count).Error; err != nil {
		t.Fatalf("count media names: %v", err)
	}
	if count != 0 {
		t.Errorf("media_names rows after a refused upload = %d, want 0", count)
	}
}
