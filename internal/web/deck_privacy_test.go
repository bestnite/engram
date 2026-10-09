package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件钉死卡组的可见性边界：一张卡组只对属主与被显式授权者可见。
// 读侧走 GET /api/v1/decks 的 JSON 列表；按 id 直连内容走 GET /decks/:id/notes。
// 历史上还存在 private/unlisted/public 三档可见性与 /sharing/visibility 写端点，二者已一并删除。

// deckListingBody 取当前会话在 GET /api/v1/decks 里看到的卡组列表 JSON 文本。
func deckListingBody(t *testing.T, srv *Server, cookies []*http.Cookie) string {
	t.Helper()
	rec := getWithCookies(t, srv, "/api/v1/decks", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/decks status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	return rec.Body.String()
}

// TestStrangerCannotSeeOrOpenAnotherUsersDeck 是这条边界的主验收：
// 别人建的卡组既不出现在列表里，也不能按 id 直连内容——没有「看得到但没有授权」的中间态。
func TestStrangerCannotSeeOrOpenAnotherUsersDeck(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	seedBasic(t, db, deck.ID, "private front", "private back")

	_, u2Cookies, _ := createUserAndLogin(t, srv, db, "stranger_viewer")

	if body := deckListingBody(t, srv, u2Cookies); strings.Contains(body, "Private deck") {
		t.Errorf("another user's deck appeared in a stranger's listing: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/notes", u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("stranger reading another user's deck by id = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestDeckVisibilityEndpointRemoved 是删除动作的回归守卫：
// /api/v1/decks/:id/sharing/visibility 路由不存在，属主写它也只会得到 404——
// 只要它还能返回 200，就说明三档可见性又回来了。
func TestDeckVisibilityEndpointRemoved(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "No visibility deck")

	path := "/api/v1/decks/" + deck.PublicID + "/sharing/visibility"
	if got := jsonRequest(t, srv, http.MethodPatch, path, `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Errorf("PATCH %s = %d, want 404 (route must be gone)", path, got.Code)
	}
	if got := jsonRequest(t, srv, http.MethodPost, path, `{"visibility":"public"}`, ownerCookies, ownerCSRF); got.Code != http.StatusNotFound {
		t.Errorf("POST %s = %d, want 404 (route must be gone)", path, got.Code)
	}
}
