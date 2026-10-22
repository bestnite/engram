package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// The canonical write entry for share links is the SPA's JSON endpoint (/api/v1/decks/:id/sharing/links);
// the page layer keeps only the app shell of GET /s/:token and the read-only content of api/v1/share/:token.

// shareLinkJSONPath is the JSON path for creating and deleting share links.
func shareLinkJSONPath(deckPublicID string) string {
	return "/api/v1/decks/" + deckPublicID + "/sharing/links"
}

// createShareLinkJSON creates a share link through the SPA JSON endpoint and returns the plaintext token; an empty password = no password.
func createShareLinkJSON(t *testing.T, srv *Server, deckPublicID string, cookies []*http.Cookie, csrf, password string) string {
	t.Helper()
	body := "{}"
	if password != "" {
		body = `{"password":` + strconv.Quote(password) + `}`
	}
	rec := jsonRequest(t, srv, http.MethodPost, shareLinkJSONPath(deckPublicID), body, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share link = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var out struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode create link response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	token := strings.TrimPrefix(out.Link, "/s/")
	if token == "" || token == out.Link {
		t.Fatalf("create link returned %q, want a /s/<token> link", out.Link)
	}
	return token
}

// shareResponse is the external shape of GET /api/v1/share/:token.
type shareResponse struct {
	DeckName         string `json:"deck_name"`
	PasswordRequired bool   `json:"password_required"`
	Notes            []struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	} `json:"notes"`
}

// TestShareLinkCreateBrowseThenRevoke is the main acceptance:
// after creation anyone can browse the content, only the digest is stored, and after revocation it is 404 at once.
func TestShareLinkCreateBrowseThenRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Link deck")
	seedBasic(t, db, deck.ID, "秘密正面", "秘密背面")

	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	// Only the sha256 digest is stored, the plaintext never lands anywhere.
	var link store.ShareLink
	if err := db.Where("deck_id = ?", deck.ID).First(&link).Error; err != nil {
		t.Fatalf("load share link row: %v", err)
	}
	if link.Token == token {
		t.Errorf("plaintext token was persisted verbatim")
	}
	if link.Token != store.ShareLinkDigest(token) {
		t.Errorf("stored token = %q, want sha256 digest %q", link.Token, store.ShareLinkDigest(token))
	}

	// Login-free browsing: /s/:token returns the app shell, and the JSON endpoint provides the content.
	browse := get(t, srv, "/s/"+token, nil)
	if browse.Code != http.StatusOK {
		t.Fatalf("anonymous GET /s/<token> status = %d, want 200 (body %s)", browse.Code, snippet(browse.Body.String()))
	}
	if !strings.Contains(browse.Body.String(), `<div id="app">`) {
		t.Errorf("share browse did not return the SPA shell: %s", snippet(browse.Body.String()))
	}
	content := get(t, srv, "/api/v1/share/"+token, nil)
	if content.Code != http.StatusOK {
		t.Fatalf("anonymous GET /api/v1/share/<token> = %d, want 200", content.Code)
	}
	var body shareResponse
	if err := json.Unmarshal(content.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode share content: %v", err)
	}
	joined := ""
	for _, n := range body.Notes {
		joined += n.FrontHTML + n.BackHTML
	}
	for _, want := range []string{"秘密正面", "秘密背面"} {
		if !strings.Contains(joined, want) {
			t.Errorf("share content missing %q: %s", want, joined)
		}
	}
	// Read-only content must carry no progress information.
	if strings.Contains(joined, "review.remaining") || strings.Contains(joined, "review.done") {
		t.Errorf("share content leaked review progress markup")
	}

	// Revoking a single link makes it 404 immediately.
	if rec := jsonRequest(t, srv, http.MethodDelete,
		shareLinkJSONPath(deck.PublicID)+"/revoke/"+link.Token, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := get(t, srv, "/s/"+token, nil).Code; got != http.StatusNotFound {
		t.Errorf("after revoke: GET /s/<token> status = %d, want 404", got)
	}
}

// TestShareLinkRevokeAll covers bulk revocation: after revoking all at once both links are 404.
func TestShareLinkRevokeAll(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Bulk deck")
	seedBasic(t, db, deck.ID, "front", "back")

	tokens := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		tokens = append(tokens, createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, ""))
	}
	if rec := jsonRequest(t, srv, http.MethodDelete, shareLinkJSONPath(deck.PublicID), "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke-all status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	for _, token := range tokens {
		if got := get(t, srv, "/s/"+token, nil).Code; got != http.StatusNotFound {
			t.Errorf("after revoke-all: GET /s/<token> status = %d, want 404", got)
		}
	}
}

// TestShareLinkPasswordGate is the acceptance's "wrong password is refused": a wrong password gives 401 with no content visible, a correct one is let through.
func TestShareLinkPasswordGate(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Locked deck")
	seedBasic(t, db, deck.ID, "口令内容正面", "口令内容背面")

	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "letmein99")

	// Before unlock only password_required comes back, leaking no content.
	locked := get(t, srv, "/api/v1/share/"+token, nil)
	if locked.Code != http.StatusOK {
		t.Fatalf("GET share content status = %d, want 200", locked.Code)
	}
	var lockedBody shareResponse
	if err := json.Unmarshal(locked.Body.Bytes(), &lockedBody); err != nil {
		t.Fatalf("decode locked share: %v", err)
	}
	if !lockedBody.PasswordRequired || len(lockedBody.Notes) != 0 {
		t.Errorf("locked share returned password_required=%t notes=%d, want true/0", lockedBody.PasswordRequired, len(lockedBody.Notes))
	}

	// Wrong password: 401, and the content is still not visible.
	bad := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"wrong-pass"}`, nil, "")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if strings.Contains(bad.Body.String(), "口令内容正面") {
		t.Errorf("content rendered despite a wrong password")
	}

	// Correct password: the content is returned.
	good := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"letmein99"}`, nil, "")
	if good.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200 (body %s)", good.Code, snippet(good.Body.String()))
	}
	if !strings.Contains(good.Body.String(), "口令内容正面") {
		t.Errorf("content missing after correct password: %s", snippet(good.Body.String()))
	}
}

// TestShareLinkExpiredReturns404 covers expiry: the resolution path treats expired and nonexistent alike.
func TestShareLinkExpiredReturns404(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Expired deck")
	past := time.Now().UTC().Add(-time.Hour)
	plaintext, _, err := store.NewShareLinkStore(db).Create(context.Background(), store.ShareLinkInput{
		DeckID: deck.ID, ExpiresAt: &past, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("seed expired share link: %v", err)
	}
	if got := get(t, srv, "/s/"+plaintext, nil).Code; got != http.StatusNotFound {
		t.Errorf("expired link status = %d, want 404", got)
	}
}

// TestShareLinkWriteRequiresOwnerAndCSRF covers the negative cases: write requests from a non-owner and without CSRF are both refused and nothing is written.
func TestShareLinkWriteRequiresOwnerAndCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Guarded deck")
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "linkviewer")
	path := shareLinkJSONPath(deck.PublicID)

	// Missing CSRF: 403, and no link is created.
	if rec := jsonRequest(t, srv, http.MethodPost, path, `{"password":"x"}`, ownerCookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d, want 403", rec.Code)
	}
	// Non-owner (no grant): 403.
	if rec := jsonRequest(t, srv, http.MethodPost, path, `{"password":"x"}`, u2Cookies, u2CSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("create by non-owner status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	links, err := store.NewShareLinkStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list share links: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("share links written despite rejected requests: %d", len(links))
	}
}
