package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers L3: "a deck a logged-in user opened through a share link counts toward their visible set, and media reads are allowed from it".
//
// The open action goes through /api/v1/share/:token (GET or POST .../unlock), which registers the session-level media grant and returns sanitized content;
// only then are the browser's subsequent GET /media/<sha256> image requests allowed through. Semantics (pinned by this file):
//   - a logged-in visitor with no grant who has not opened the share link → that deck's media is 404;
//   - after a successful open (no-password link, or correct password) → media referenced by that deck is 200;
//   - a link with a wrong password registers no grant; a revoked / expired link yields no media even if it was opened before;
//   - the grant covers only the shared deck: media of other decks, and media no note references, still give 404;
//   - the grant hangs off the **server-side session**: another session (even of the same user) does not get media either.
//
// Fixtures are always built through the real write paths (NoteStore writing notes, media uploaded over HTTP), so what is tested is the production path.

// visitorSessionID takes the id of a user's most recent session (used by the tests to assert on / locate share grant rows).
func visitorSessionID(t *testing.T, db *gorm.DB, userID uint64) string {
	t.Helper()
	var sess store.Session
	if err := db.Where("user_id = ?", userID).Order("created_at desc, id desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row for user %d: %v", userID, err)
	}
	return sess.ID
}

// shareGrantRows returns the number of share grant rows for (session_id, deck_id).
func shareGrantRows(t *testing.T, db *gorm.DB, sessionID string, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Table("share_session_decks").
		Where("session_id = ? AND deck_id = ?", sessionID, deckID).Count(&n).Error; err != nil {
		t.Fatalf("count share_session_decks: %v", err)
	}
	return n
}

// seedReferencedMediaDeck creates a deck for owner, uploads one piece of media, and has a note in the deck reference it;
// tag gives different decks different sha256 values (content addressing: different bytes, different sha).
func seedReferencedMediaDeck(t *testing.T, srv *Server, db *gorm.DB, ownerID uint64, ownerCookies []*http.Cookie, ownerCSRF, name string, tag byte) (*store.Deck, string) {
	t.Helper()
	deck := seedDeck(t, db, ownerID, name)
	sha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, append(pngBody(), tag))
	referenceMediaBy(t, db, deck.ID, &ownerID, sha)
	return deck, sha
}

// createShareLinkForDeck creates a share link and returns the plaintext token; an empty password = no password.
// Keeps the call shape existing tests use; internally it goes through the SPA's JSON endpoints.
func createShareLinkForDeck(t *testing.T, srv *Server, deckPublicID string, ownerCookies []*http.Cookie, ownerCSRF, password string) string {
	t.Helper()
	return createShareLinkJSON(t, srv, deckPublicID, ownerCookies, ownerCSRF, password)
}

// openShareLink opens a no-password share link as a visitor (GET /api/v1/share/:token), registering the media grant.
func openShareLink(t *testing.T, srv *Server, token string, cookies []*http.Cookie) {
	t.Helper()
	rec := getWithCookies(t, srv, "/api/v1/share/"+token, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/share/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestShareLinkMediaReadableAfterBrowse is the main L3 acceptance (no-password link):
// a logged-in visitor who has not opened the share link cannot read a private deck's media; once the open succeeds the same session can read it.
func TestShareLinkMediaReadableAfterBrowse(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 open deck", 'a')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-open")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	// 1) Share link not opened yet: no grant → 404.
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before browse: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) Opening the no-password share link succeeds → the grant is registered.
	openShareLink(t, srv, token, visitorCookies)

	// 3) The grant is registered on the visitor's own session, so the media becomes readable.
	sessionID := visitorSessionID(t, db, visitorID)
	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 1 {
		t.Errorf("share_session_decks rows = %d, want 1", n)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after browse: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaGrantExpiryIsMinOfLinkAndSession asserts a grant row expires at the earlier of "link expiry" and "session expiry".
func TestShareLinkMediaGrantExpiryIsMinOfLinkAndSession(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, _ := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 expiry deck", 'b')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-expiry")

	linkExp := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	plaintext, _, err := store.NewShareLinkStore(db).Create(t.Context(), store.ShareLinkInput{
		DeckID: deck.ID, ExpiresAt: &linkExp, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("seed share link: %v", err)
	}
	openShareLink(t, srv, plaintext, visitorCookies)

	var row struct {
		ExpiresAt time.Time
	}
	if err := db.Table("share_session_decks").
		Select("expires_at").
		Where("session_id = ? AND deck_id = ?", visitorSessionID(t, db, visitorID), deck.ID).
		Scan(&row).Error; err != nil {
		t.Fatalf("load share_session_decks row: %v", err)
	}
	if !row.ExpiresAt.Equal(linkExp) {
		t.Errorf("grant expires_at = %s, want link expiry %s (min of link and session)", row.ExpiresAt, linkExp)
	}
}

// TestShareLinkPasswordGateForMedia covers password-protected links: not unlocked 404, wrong password 404, correct password 200.
func TestShareLinkPasswordGateForMedia(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 locked deck", 'c')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-locked")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "letmein99")
	sessionID := visitorSessionID(t, db, visitorID)

	// 1) Not unlocked, no grant → 404.
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before unlock: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) Wrong password → 401, no grant registered → 404.
	bad := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"wrong-pass"}`, visitorCookies, "")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 0 {
		t.Errorf("wrong password recorded %d share grants, want 0", n)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after wrong password: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 3) Correct password → 200, and the grant is registered along with it.
	good := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"letmein99"}`, visitorCookies, "")
	if good.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200 (body %s)", good.Code, snippet(good.Body.String()))
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after unlock: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaLapsesAfterRevoke covers revocation: for a session that opened before (grant registered), media must be 404 immediately once the link is revoked.
func TestShareLinkMediaLapsesAfterRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 revoke deck", 'd')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-revoke")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("visitor GET %s before revoke = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	var link store.ShareLink
	if err := db.Where("deck_id = ?", deck.ID).First(&link).Error; err != nil {
		t.Fatalf("load share link: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodDelete,
		shareLinkJSONPath(deck.PublicID)+"/revoke/"+link.Token, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaLapsesAfterLinkExpiry covers expiry: the link is valid when opened and expires afterwards, and the registered grant must no longer let media through.
func TestShareLinkMediaLapsesAfterLinkExpiry(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 expiry lapse deck", 'e')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-lapse")
	target := "/media/" + sha

	linkExp := time.Now().UTC().Add(time.Hour)
	plaintext, _, err := store.NewShareLinkStore(db).Create(t.Context(), store.ShareLinkInput{
		DeckID: deck.ID, ExpiresAt: &linkExp, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("seed share link: %v", err)
	}
	openShareLink(t, srv, plaintext, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("visitor GET %s before expiry = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// Expire the link (push expires_at into the past in the real DB, equivalent to time moving forward).
	if err := db.Model(&store.ShareLink{}).Where("deck_id = ?", deck.ID).
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire share link: %v", err)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after link expiry: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaCoversOnlyTheSharedDeck covers the scope: the grant covers only references inside the shared deck.
// After the visitor opens A's share link, media of deck B (also owned by owner) and media no note references must both be 404.
func TestShareLinkMediaCoversOnlyTheSharedDeck(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deckA, shaA := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 scope A", 'f')
	_, shaB := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 scope B", 'g')
	// One piece of media no note references (uploaded by owner, referenced by nobody).
	unreferenced := uploadAndSha(t, srv, ownerCookies, ownerCSRF, append(pngBody(), 'h'))

	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-scope")
	token := createShareLinkJSON(t, srv, deckA.PublicID, ownerCookies, ownerCSRF, "")
	openShareLink(t, srv, token, visitorCookies)

	if rec := getWithCookies(t, srv, "/media/"+shaA, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("shared deck media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, "/media/"+shaB, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("other deck media = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, "/media/"+unreferenced, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unreferenced media = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaGrantIsSessionScoped covers session isolation: the grant hangs off the session that opened the share link,
// and another session (of the same user, which never opened it) does not get media.
func TestShareLinkMediaGrantIsSessionScoped(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 session scope", 'i')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-session")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("opening session GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// A session of another account (which never opened the share link) is still 404.
	_, otherCookies, _ := createUserAndLogin(t, srv, db, "l3-other-session")
	if rec := getWithCookies(t, srv, target, otherCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unrelated session GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkJoinGrantsReaderAccess pins the semantics of "opening the link is read-only, joining takes a button press":
//   - opening the link: media is allowed (session-level read-only), but the deck does **not** enter the visitor's list and no grant row is written;
//   - POST /api/v1/share/:token/join: gets an explicit reader grant and the deck enters their list;
//   - the owner pressing join themselves is idempotent and produces no grant row; pressing join while logged out is refused by the session/CSRF gate (403);
//   - a third account that never opened the link cannot read media; an anonymous visitor can still browse /s/:token directly.
func TestShareLinkJoinGrantsReaderAccess(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 join deck", 'j')
	visitorID, visitorCookies, visitorCSRF := createUserAndLogin(t, srv, db, "l3-visitor-join")
	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "l3-stranger-join")
	target := "/media/" + sha
	joinPath := "/api/v1/share/" + createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "") + "/join"
	token := strings.TrimSuffix(strings.TrimPrefix(joinPath, "/api/v1/share/"), "/join")

	// 1) Before opening the link: it is not in the list, media is 404.
	if body := deckListingBody(t, srv, visitorCookies); strings.Contains(body, deck.Name) {
		t.Fatalf("deck appeared in the visitor's listing before anything was opened: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before browse: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) Opening the link: read-only browse + session-level media allowance; but "opening once" is not joining.
	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after browse: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
	if body := deckListingBody(t, srv, visitorCookies); strings.Contains(body, deck.Name) {
		t.Fatalf("opening the link must not add the deck to the visitor's listing: %s", snippet(body))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, visitorID); err != nil || role != "" {
		t.Fatalf("opening the link wrote a grant row: role=%q err=%v, want none", role, err)
	}

	// 3) Joining: only an explicit join writes the grant, and the deck then enters the list.
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", visitorCookies, visitorCSRF); rec.Code != http.StatusOK {
		t.Fatalf("join status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if body := deckListingBody(t, srv, visitorCookies); !strings.Contains(body, deck.Name) {
		t.Fatalf("deck missing from the visitor's listing after joining: %s", snippet(body))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, visitorID); err != nil || role != store.RoleReader {
		t.Fatalf("grant role after join = %q err=%v, want %q", role, err, store.RoleReader)
	}

	// 4) The owner pressing join themselves: idempotent success, no grant row produced (the owner column is already the single truth).
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("owner join status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, ownerID); err != nil || role != "" {
		t.Fatalf("owner got a grant row from joining their own deck: role=%q err=%v, want none", role, err)
	}

	// 5) Pressing join while logged out: the session/CSRF gate refuses first, and no grant row is left behind.
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous join status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 6) A third account that never opened the link: media is still 404.
	if rec := getWithCookies(t, srv, target, strangerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unrelated user GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 7) Anonymous visitors can still browse the share link directly.
	if rec := get(t, srv, "/s/"+token, nil); rec.Code != http.StatusOK {
		t.Fatalf("anonymous GET /s/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}
