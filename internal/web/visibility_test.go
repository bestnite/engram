package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"example.com/engram/internal/store"
)

// setVisibility 通过共享管理页的可见性表单修改卡组可见性。
func setVisibility(t *testing.T, srv *Server, deckID uint64, visibility string, cookies []*http.Cookie, csrf string) {
	t.Helper()
	rec := postForm(t, srv, "/decks/"+u64str(deckID)+"/sharing/visibility", url.Values{
		"csrf_token": {csrf}, "visibility": {visibility},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("set visibility %q status = %d, want 303 (body %s)", visibility, rec.Code, snippet(rec.Body.String()))
	}
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
	list := getWithCookies(t, srv, "/decks", u2Cookies)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200", list.Code)
	}
	if strings.Contains(list.Body.String(), "Unlisted deck") {
		t.Errorf("unlisted deck appeared in the deck listing")
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

	list := getWithCookies(t, srv, "/decks", u2Cookies)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200", list.Code)
	}
	if !strings.Contains(list.Body.String(), "Public deck") {
		t.Errorf("public deck missing from a signed-in user's listing")
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

	list := getWithCookies(t, srv, "/decks", u2Cookies)
	if strings.Contains(list.Body.String(), "Private deck") {
		t.Errorf("private deck appeared in a stranger's listing")
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
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/visibility", url.Values{
		"csrf_token": {u2CSRF}, "visibility": {store.DeckVisibilityPublic},
	}, u2Cookies); rec.Code != http.StatusForbidden {
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
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/visibility", url.Values{
		"csrf_token": {ownerCSRF}, "visibility": {"everyone"},
	}, ownerCookies); rec.Code != http.StatusBadRequest {
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
