package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file is the acceptance for the SPA migration of public read-only share browsing (internal/web/share_api.go).
//
// Covers: the shell switch on GET /s/:token, revoked/unknown links still 404, JSON content reusing the server-side sanitizing render,
// the password gate (no body before unlock), and the session-level media grant registered via JSON on a successful open. Real routes and SQLite.

// shareResponse is the response shape of GET /api/v1/share/:token (for test decoding).
type shareResponseFixture struct {
	DeckName         string `json:"deck_name"`
	PasswordRequired bool   `json:"password_required"`
	Notes            []struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	} `json:"notes"`
}

func decodeShareResponse(t *testing.T, rec *httptest.ResponseRecorder) shareResponseFixture {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("share JSON status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body shareResponseFixture
	decodeJSON(t, rec, &body)
	return body
}

// TestShareBrowseServesShell covers the app shell on GET /s/:token and the 404 for an unknown link.
func TestShareBrowseServesShell(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Cutover share deck")
	seedBasic(t, db, deck.ID, "正面", "背面")
	token := createShareLinkForDeck(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	assertShell(t, get(t, srv, "/s/"+token, nil))

	// An unknown link is still 404: the reachability decision happens before the shell is served.
	if rec := get(t, srv, "/s/does-not-exist-token-1234567890", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /s/<unknown> = %d, want 404", rec.Code)
	}
}

// TestShareJSONContent asserts the JSON content endpoint returns server-sanitized front and back HTML, and an unknown link is 404.
func TestShareJSONContent(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "JSON share deck")
	seedBasic(t, db, deck.ID, "安全正面", "安全背面")
	seedBasic(t, db, deck.ID, `<script>alert(1)</script>`, "背面")
	token := createShareLinkForDeck(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	body := decodeShareResponse(t, get(t, srv, "/api/v1/share/"+token, nil))
	if body.DeckName != "JSON share deck" {
		t.Errorf("deck_name = %q, want JSON share deck", body.DeckName)
	}
	if body.PasswordRequired {
		t.Errorf("password_required = true for a link without a password")
	}
	if len(body.Notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(body.Notes))
	}
	joined := body.Notes[0].FrontHTML + body.Notes[0].BackHTML + body.Notes[1].FrontHTML
	if !strings.Contains(joined, "安全正面") {
		t.Errorf("JSON content is missing the card front: %s", joined)
	}
	if strings.Contains(joined, "<script>") {
		t.Errorf("JSON content leaked an unsanitized <script>: %s", joined)
	}

	if rec := get(t, srv, "/api/v1/share/unknown-token-abcdefghij", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/share/<unknown> = %d, want 404", rec.Code)
	}
}

// TestShareJSONPasswordGate asserts a password-protected link returns no body before unlock, a wrong password gives 401 and a correct one 200.
func TestShareJSONPasswordGate(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Locked share deck")
	seedBasic(t, db, deck.ID, "机密正面", "机密背面")
	token := createShareLinkForDeck(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "letmein99")

	locked := decodeShareResponse(t, get(t, srv, "/api/v1/share/"+token, nil))
	if !locked.PasswordRequired || len(locked.Notes) != 0 {
		t.Fatalf("locked link must not return content: %+v", locked)
	}

	bad := postJSON(srv, "/api/v1/share/"+token+"/unlock", map[string]string{"password": "wrong-pass"}, nil, nil)
	if bad.Code != http.StatusUnauthorized || apiErrorCode(t, bad) != "share_password_invalid" {
		t.Fatalf("wrong password = %d %s, want 401 share_password_invalid", bad.Code, apiErrorCode(t, bad))
	}

	good := postJSON(srv, "/api/v1/share/"+token+"/unlock", map[string]string{"password": "letmein99"}, nil, nil)
	unlocked := decodeShareResponse(t, good)
	if unlocked.PasswordRequired || len(unlocked.Notes) != 1 || !strings.Contains(unlocked.Notes[0].FrontHTML, "机密正面") {
		t.Errorf("correct password did not return the deck content: %+v", unlocked)
	}
}

// TestShareJSONRecordsMediaGrant asserts that after opening the share page successfully via JSON, the logged-in visitor can read media in the same session
// (same source as SSR's recordShareGrant, so the media grant is not lost to the shell switch).
func TestShareJSONRecordsMediaGrant(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "JSON media deck", 'a')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "spa-json-visitor")
	token := createShareLinkForDeck(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")
	sessionID := visitorSessionID(t, db, visitorID)

	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 0 {
		t.Fatalf("share_session_decks rows before opening = %d, want 0", n)
	}

	rec := getWithCookies(t, srv, "/api/v1/share/"+token, visitorCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("JSON share get = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 1 {
		t.Fatalf("share_session_decks rows after opening = %d, want 1", n)
	}
	media := getWithCookies(t, srv, "/media/"+sha, visitorCookies)
	if media.Code != http.StatusOK {
		t.Errorf("visitor GET /media/<sha> after JSON browse = %d, want 200", media.Code)
	}
}
