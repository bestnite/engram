package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckCloneCopiesContentWithoutProgress is the main acceptance test:
// after cloning, the note/card counts match, card_states under the new owner is 0, and the source deck is unaffected.
func TestDeckCloneCopiesContentWithoutProgress(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Source deck")
	// Three note types: basic (1 card), basic_both (2 cards), cloze (2 cards) — covering one note with multiple cards.
	seedBasic(t, db, deck.ID, "Q1", "A1", "tag-one")
	both := &store.Note{DeckID: deck.ID, Kind: "basic_both"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), both, map[string]any{"front": "Q2", "back": "A2"}); err != nil {
		t.Fatalf("create basic_both: %v", err)
	}
	cloze := &store.Note{DeckID: deck.ID, Kind: "cloze"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), cloze, map[string]any{"text": "{{c1::one}} and {{c2::two}}"}); err != nil {
		t.Fatalf("create cloze: %v", err)
	}

	srcNotes := countNotes(t, db, deck.ID)
	srcCards := countCards(t, db, deck.ID)
	if srcNotes != 3 || srcCards != 5 {
		t.Fatalf("seed produced (%d notes, %d cards), want (3, 5)", srcNotes, srcCards)
	}

	// The owner clones their own deck.
	rec := postForm(t, srv, "/decks/"+deck.PublicID+"/clone", url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckPublicIDFromLocation(t, rec.Header().Get("Location"))

	// The new deck belongs to the caller, its name carries the copy suffix, and it keeps the preset in effect for the caller on the source deck (the owner is the source deck's preset holder).
	var cloned store.Deck
	if err := db.First(&cloned, "public_id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != ownerID {
		t.Errorf("cloned deck owner = %d, want %d", cloned.OwnerUserID, ownerID)
	}
	if cloned.ID == deck.ID {
		t.Fatal("clone reused the source deck id")
	}
	if cloned.PresetID != deck.PresetID {
		t.Errorf("owner clone preset = %d, want the owner's own preset %d", cloned.PresetID, deck.PresetID)
	}
	if cloned.Name == deck.Name || !strings.Contains(cloned.Name, deck.Name) {
		t.Errorf("cloned deck name = %q, want it to contain %q", cloned.Name, deck.Name)
	}

	// Content counts match.
	if got := countNotes(t, db, cloned.ID); got != srcNotes {
		t.Errorf("cloned note count = %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, cloned.ID); got != srcCards {
		t.Errorf("cloned card count = %d, want %d", got, srcCards)
	}

	// Progress is zero: the new owner has no card_states rows on the new cards.
	var states int64
	if err := db.Model(&store.CardState{}).
		Where("user_id = ? AND card_id IN (?)", ownerID, cardIDSubquery(db, cloned.ID)).
		Count(&states).Error; err != nil {
		t.Fatalf("count cloned card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("cloned deck carries progress: card_states rows = %d, want 0", states)
	}

	// The source deck is unaffected: counts unchanged.
	if got := countNotes(t, db, deck.ID); got != srcNotes {
		t.Errorf("source note count changed to %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, deck.ID); got != srcCards {
		t.Errorf("source card count changed to %d, want %d", got, srcCards)
	}

	// Cloning also writes one audit row.
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckClone)
	if err != nil {
		t.Fatalf("count clone audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckClone, n)
	}
}

// TestDeckCloneAllowedForReader covers "readable means cloneable": a granted reader can copy the content under their own name.
func TestDeckCloneAllowedForReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Readable deck")
	seedBasic(t, db, deck.ID, "Q", "A")
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "cloner2")
	grantRole(t, srv, deck.ID, user2ID, store.RoleReader, ownerCookies, ownerCSRF)

	rec := postForm(t, srv, "/decks/"+deck.PublicID+"/clone", url.Values{"csrf_token": {u2CSRF}}, u2Cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reader POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckPublicIDFromLocation(t, rec.Header().Get("Location"))
	var cloned store.Deck
	if err := db.First(&cloned, "public_id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != user2ID {
		t.Errorf("cloned deck owner = %d, want reader id %d", cloned.OwnerUserID, user2ID)
	}
	if got := countNotes(t, db, cloned.ID); got != 1 {
		t.Errorf("cloned note count = %d, want 1", got)
	}
	var states int64
	if err := db.Model(&store.CardState{}).Where("user_id = ?", user2ID).Count(&states).Error; err != nil {
		t.Fatalf("count reader card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("reader clone carries progress: %d rows, want 0", states)
	}
	// Counter-case: cloning someone else's deck does not carry over the owner's preset (parameters and optimized weights); the reader's own preset is used.
	var preset store.Preset
	if err := db.First(&preset, "id = ?", cloned.PresetID).Error; err != nil {
		t.Fatalf("load cloned deck preset: %v", err)
	}
	if preset.OwnerUserID != user2ID {
		t.Errorf("cloned deck preset belongs to user %d, want the reader %d", preset.OwnerUserID, user2ID)
	}
}

// countNotes counts the non-soft-deleted notes in a deck.
func countNotes(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&n).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	return n
}

// countCards counts the visible cards in a deck (joining notes to exclude soft-deleted content).
func countCards(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID).
		Count(&n).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	return n
}

// cardIDSubquery returns a subquery for the card ids in a deck, used to count card_states.
func cardIDSubquery(db *gorm.DB, deckID uint64) *gorm.DB {
	return db.Table("cards").
		Select("cards.id").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID)
}

// parseDeckPublicIDFromLocation extracts the deck's public id from a Location of the form /decks/<id>/notes.
func parseDeckPublicIDFromLocation(t *testing.T, location string) string {
	t.Helper()
	parts := strings.Split(strings.Trim(location, "/"), "/")
	if len(parts) < 2 || parts[0] != "decks" || parts[1] == "" {
		t.Fatalf("unexpected clone redirect Location %q", location)
	}
	return parts[1]
}

// TestCloneAPIAllowsOwnerEditorReader asserts owner/editor/reader can all use
// POST /api/v1/decks/:id/clone to clone the deck under their own name, that the response is the new deck's JSON {id,name},
// and that the new deck belongs to the caller (progress not following is covered by clone_test.go).
func TestCloneAPIAllowsOwnerEditorReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone API source")
	seedBasic(t, db, deck.ID, "Q", "A")
	path := "/api/v1/decks/" + deck.PublicID + "/clone"

	editorID, editorCookies, editorCSRF := createUserAndLogin(t, srv, db, "clone-editor")
	grantRole(t, srv, deck.ID, editorID, store.RoleEditor, ownerCookies, ownerCSRF)
	readerID, readerCookies, readerCSRF := createUserAndLogin(t, srv, db, "clone-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)

	for _, tc := range []struct {
		name    string
		userID  uint64
		cookies []*http.Cookie
		csrf    string
	}{
		{"owner", ownerID, ownerCookies, ownerCSRF},
		{"editor", editorID, editorCookies, editorCSRF},
		{"reader", readerID, readerCookies, readerCSRF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postAccept(t, srv, path, "{}", "application/json", tc.cookies, tc.csrf)
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST clone (%s) = %d, want 201 (body %s)", tc.name, rec.Code, snippet(rec.Body.String()))
			}
			var body cloneResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode clone response: %v (body %s)", err, rec.Body.String())
			}
			if body.ID == "" || body.ID == deck.PublicID {
				t.Errorf("clone (%s) id = %q, want a new non-empty id", tc.name, body.ID)
			}
			var cloned store.Deck
			if err := db.First(&cloned, "public_id = ?", body.ID).Error; err != nil {
				t.Fatalf("load cloned deck: %v", err)
			}
			if cloned.OwnerUserID != tc.userID {
				t.Errorf("clone (%s) owner = %d, want %d", tc.name, cloned.OwnerUserID, tc.userID)
			}
		})
	}
}

// TestCloneAPIRejectsNonReaderAndMissingCSRF asserts the clone API's two rejection paths:
// a stranger with no grants gets 403; a missing CSRF token gets 403; neither produces a new deck.
func TestCloneAPIRejectsNonReaderAndMissingCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone guard source")
	path := "/api/v1/decks/" + deck.PublicID + "/clone"

	var before int64
	if err := db.Model(&store.Deck{}).Count(&before).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}

	_, strangerCookies, strangerCSRF := createUserAndLogin(t, srv, db, "clone-stranger")
	if rec := postAccept(t, srv, path, "{}", "application/json", strangerCookies, strangerCSRF); rec.Code != http.StatusForbidden {
		t.Errorf("stranger POST clone = %d, want 403", rec.Code)
	}
	if rec := postAccept(t, srv, path, "{}", "application/json", ownerCookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("clone without CSRF = %d, want 403", rec.Code)
	}
	// With a correct token the owner can still clone, proving the first two failures are authorization/CSRF rather than a wrong path.
	if rec := postAccept(t, srv, path, "{}", "application/json", ownerCookies, ownerCSRF); rec.Code != http.StatusCreated {
		t.Fatalf("owner POST clone with CSRF = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	var after int64
	if err := db.Model(&store.Deck{}).Count(&after).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if after != before+1 {
		t.Errorf("deck count = %d, want %d (only the authorised clone persisted)", after, before+1)
	}
}

// TestCloneAcceptHeaderChoosesJSONOrRedirect asserts the clone endpoint's response shape is decided by Accept:
// application/json with parameters (a client may append a charset) still returns 201 JSON, while a browser form-style Accept
// still gets the 303 redirect to the new deck — avoiding a JSON call being misjudged as a form submission and following the 303 to HTML.
func TestCloneAcceptHeaderChoosesJSONOrRedirect(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone accept source")
	path := "/api/v1/decks/" + deck.PublicID + "/clone"

	jsonRec := postAccept(t, srv, path, "{}", "application/json; charset=utf-8", ownerCookies, ownerCSRF)
	if jsonRec.Code != http.StatusCreated {
		t.Fatalf("clone with charset Accept = %d, want 201 (body %s)", jsonRec.Code, snippet(jsonRec.Body.String()))
	}
	if ct := jsonRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("clone Content-Type = %q, want application/json", ct)
	}

	htmlRec := postAccept(t, srv, path, "", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", ownerCookies, ownerCSRF)
	if htmlRec.Code != http.StatusSeeOther {
		t.Fatalf("clone with browser Accept = %d, want 303 (body %s)", htmlRec.Code, snippet(htmlRec.Body.String()))
	}
	if loc := htmlRec.Header().Get("Location"); !strings.HasPrefix(loc, "/decks/") || !strings.HasSuffix(loc, "/notes") {
		t.Errorf("clone redirect Location = %q, want /decks/<id>/notes", loc)
	}
}

// postAccept issues a POST with an explicit Accept, to assert whether Accept decides on a JSON response or a 303 redirect.
func postAccept(t *testing.T, srv *Server, target, body, accept string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", accept)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// cloneResponse is the clone endpoint's JSON response body.
type cloneResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestCloneTruncatesNearLimitSourceName is the end-to-end acceptance for AUDIT-05: a legal source name close to the limit still
// produces a legal copy name after cloning — the suffix fits and the whole name stays within 200 characters (before the fix the copy name would be over-long, and every rename or
// bundle-import entry point would reject it afterwards).
func TestCloneTruncatesNearLimitSourceName(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	longName := strings.Repeat("卡", 200) // exactly at the limit, a legal source name
	deck := seedDeck(t, db, ownerID, longName)
	seedBasic(t, db, deck.ID, "Q", "A")

	rec := postAccept(t, srv, "/api/v1/decks/"+deck.PublicID+"/clone", "{}", "application/json", cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone near-limit name = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body cloneResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode clone response: %v (body %s)", err, rec.Body.String())
	}
	var cloned store.Deck
	if err := db.First(&cloned, "public_id = ?", body.ID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if n := utf8.RuneCountInString(cloned.Name); n > 200 {
		t.Errorf("cloned name has %d runes, want <= 200 (%q)", n, cloned.Name)
	}
	if !strings.HasSuffix(cloned.Name, "（副本）") {
		t.Errorf("cloned name = %q, want the localized copy suffix", cloned.Name)
	}
}
