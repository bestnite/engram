package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckSettingsDescriptionMarkdownOwnerOnly pins the deck-wide description format: the owner round-trips it through the
// settings PATCH and the audit log, while a sharing member is refused before any write because the column belongs to the deck.
func TestDeckSettingsDescriptionMarkdownOwnerOnly(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA markdown deck")

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":20,"reviews_per_day":200,"description_markdown":true}`, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if body := decodeDeckSettings(t, rec.Body.Bytes()); !body.DescriptionMarkdown {
		t.Errorf("PATCH response description_markdown = false, want true")
	}
	stored, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if !stored.DescriptionIsMarkdown() {
		t.Errorf("stored description_markdown = false, want true")
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckDescriptionMarkdown); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckDescriptionMarkdown, n)
	}

	memberID, memberCookies, memberCSRF := createUserAndLogin(t, srv, db, "markdown_member")
	grantRole(t, srv, deck.ID, memberID, store.RoleReader, ownerCookies, ownerCSRF)
	rec = jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":20,"reviews_per_day":200,"description_markdown":false}`, memberCookies, memberCSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member PATCH description_markdown = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	stored, err = store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if !stored.DescriptionIsMarkdown() {
		t.Errorf("member changed the deck-wide description format")
	}
}

// TestSPAShellCarriesNoDeckDescription asserts the app shell document never inlines a deck description: the SPA fetches it as
// JSON and can render it only inside the sandboxed frame, so a stored <script> in the description cannot run in the page.
func TestSPAShellCarriesNoDeckDescription(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA shell deck")
	const payload = `<script>window.__deck_description_ran = true</script>`
	if err := db.WithContext(context.Background()).Model(&store.Deck{}).Where("id = ?", deck.ID).
		Updates(map[string]any{"description": payload}).Error; err != nil {
		t.Fatalf("store deck description: %v", err)
	}

	rec := getWithCookies(t, srv, "/decks/"+deck.PublicID, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET deck shell = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), "window.__deck_description_ran") {
		t.Errorf("SPA shell inlined the deck description")
	}
}
