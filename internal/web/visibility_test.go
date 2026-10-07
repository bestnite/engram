package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 可见性的读侧（谁能在列表里看到）走 GET /api/v1/decks 的 JSON 列表；写侧走
// PATCH /api/v1/decks/:id/sharing/visibility。页面层不再渲染 SSR 列表页。

// setVisibility 通过 SPA JSON 端点修改卡组可见性。
func setVisibility(t *testing.T, srv *Server, deckID uint64, visibility string, cookies []*http.Cookie, csrf string) {
	t.Helper()
	rec := jsonRequest(t, srv, http.MethodPatch,
		"/api/v1/decks/"+u64str(deckID)+"/sharing/visibility",
		`{"visibility":"`+visibility+`"}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("set visibility %q status = %d, want 200 (body %s)", visibility, rec.Code, snippet(rec.Body.String()))
	}
}

// deckListingBody 取当前会话在 GET /api/v1/decks 里看到的卡组列表 JSON 文本。
func deckListingBody(t *testing.T, srv *Server, cookies []*http.Cookie) string {
	t.Helper()
	rec := getWithCookies(t, srv, "/api/v1/decks", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/decks status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	return rec.Body.String()
}

// TestUnlistedDeckHiddenFromListsButReachableByID 是 M5-5 的主验收：
// unlisted 永不出现在任何列表里，但能按直接 id 访问。
func TestUnlistedDeckHiddenFromListsButReachableByID(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Unlisted deck")
	seedBasic(t, db, deck.ID, "unlisted front", "unlisted back")

	setVisibility(t, srv, deck.ID, store.DeckVisibilityUnlisted, ownerCookies, ownerCSRF)

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "unlisted_viewer")

	// 列表：不出现。
	if body := deckListingBody(t, srv, u2Cookies); strings.Contains(body, "Unlisted deck") {
		t.Errorf("unlisted deck appeared in the deck listing: %s", snippet(body))
	}
	// 直接 id：可见（拿到链接可看）。
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Errorf("unlisted deck by direct id status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestPublicDeckVisibleInListingToSignedInUser 覆盖 public：登录用户的列表里出现，且能直接访问。
func TestPublicDeckVisibleInListingToSignedInUser(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Public deck")
	seedBasic(t, db, deck.ID, "public front", "public back")

	setVisibility(t, srv, deck.ID, store.DeckVisibilityPublic, ownerCookies, ownerCSRF)

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "public_viewer")

	if body := deckListingBody(t, srv, u2Cookies); !strings.Contains(body, "Public deck") {
		t.Errorf("public deck missing from a signed-in user's listing: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", u2Cookies); rec.Code != http.StatusOK {
		t.Errorf("public deck by direct id status = %d, want 200", rec.Code)
	}
}

// TestPrivateDeckStaysHidden 覆盖 private：既不在列表里，也不能按 id 访问。
func TestPrivateDeckStaysHidden(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	seedBasic(t, db, deck.ID, "private front", "private back")

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "private_viewer")

	if body := deckListingBody(t, srv, u2Cookies); strings.Contains(body, "Private deck") {
		t.Errorf("private deck appeared in a stranger's listing: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("private deck by direct id status = %d, want 403", rec.Code)
	}
}

// TestVisibilityChangeOwnerOnlyAndAudited 覆盖反面用例与审计：非 owner 改不动可见性，owner 改一次写一行审计。
func TestVisibilityChangeOwnerOnlyAndAudited(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Audited deck")
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "visitor")

	// 非 owner（且无授权）：403，且可见性不变。
	if rec := jsonRequest(t, srv, http.MethodPatch,
		"/api/v1/decks/"+u64str(deck.ID)+"/sharing/visibility",
		`{"visibility":"public"}`, u2Cookies, u2CSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner visibility change status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	got, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if got.Visibility != store.DeckVisibilityPrivate {
		t.Errorf("visibility changed by non-owner: %q", got.Visibility)
	}

	// 非法取值：400。
	if rec := jsonRequest(t, srv, http.MethodPatch,
		"/api/v1/decks/"+u64str(deck.ID)+"/sharing/visibility",
		`{"visibility":"everyone"}`, ownerCookies, ownerCSRF); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid visibility status = %d, want 400", rec.Code)
	}

	// owner 合法修改：写一行审计。
	setVisibility(t, srv, deck.ID, store.DeckVisibilityPublic, ownerCookies, ownerCSRF)
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckVisibility)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Errorf("deck.visibility_change audit rows = %d, want 1", n)
	}
}
