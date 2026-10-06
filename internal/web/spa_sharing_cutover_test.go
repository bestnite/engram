package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 GET /decks/:id/sharing 的 SPA 规范路径切流与克隆入口（DESIGN.md §5、§8.1、§8.5）。
//
// 共享管理页（owner 专属）在 SPA 已加载时返回应用壳，由客户端路由渲染，数据走
// /api/v1/decks/:id/sharing 下的 JSON 端点；判权与迁移前的 SSR 页逐项一致（先要求已登录会话，
// 再按 owner 角色判定），SPA 缺失（降级）时回退 SSR 共享页。
//
// 克隆是「自己可读即可克隆」的独立工作流（reader 及以上），SPA 卡组详情页通过
// POST /api/v1/decks/:id/clone 调用；本文件钉住 owner/editor/reader 三种体验、非读者与缺 CSRF
// 的拒绝，以及 Accept 决定返回 JSON 还是 303 重定向。
//
// 公开分享浏览 /s/:token 仍是 SSR（未随本切流迁移），本文件用一条测试把这条边界钉死。
// SSR handler、模板与全部写路径（POST）保持不变。

// ssrSharingMarker 是 SSR 共享页特有的表单标记：SPA 应用壳里不会出现。
func ssrSharingMarker(deckID uint64) string {
	return `action="/decks/` + u64str(deckID) + `/sharing/grant"`
}

// TestSharingRouteServesSPAShellForOwner 断言 owner 访问 GET /decks/:id/sharing 得到 SPA 应用壳，
// 由客户端路由渲染共享管理页，不再渲染 SSR 共享页。
func TestSharingRouteServesSPAShellForOwner(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Sharing shell deck")

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/sharing", cookies)
	assertSPAShell(t, rec)
	if strings.Contains(rec.Body.String(), ssrSharingMarker(deck.ID)) {
		t.Errorf("GET sharing still renders the SSR page: %s", snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `name="username"`) {
		t.Errorf("GET sharing still renders the SSR grant form: %s", snippet(rec.Body.String()))
	}
}

// TestSharingRouteDeniesNonOwnerAndAnonymous 断言切壳不改动授权：匿名 303 重定向登录页；
// 被授予 editor/reader 的用户与陌生用户都仍回 403，共享壳不会交给无权用户。
func TestSharingRouteDeniesNonOwnerAndAnonymous(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Owner only sharing")
	path := "/decks/" + u64str(deck.ID) + "/sharing"

	anonymous := getWithCookies(t, srv, path, nil)
	if anonymous.Code != http.StatusSeeOther || anonymous.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous GET sharing = %d loc %q, want 303 /login", anonymous.Code, anonymous.Header().Get("Location"))
	}

	editorID, editorCookies, _ := createUserAndLogin(t, srv, db, "sharing-editor")
	grantRole(t, srv, deck.ID, editorID, store.RoleEditor, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, path, editorCookies); rec.Code != http.StatusForbidden {
		t.Errorf("editor GET sharing = %d, want 403", rec.Code)
	}

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "sharing-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, path, readerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("reader GET sharing = %d, want 403", rec.Code)
	}

	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "sharing-stranger")
	if rec := getWithCookies(t, srv, path, strangerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("stranger GET sharing = %d, want 403", rec.Code)
	}
}

// TestSharingRouteFallsBackToSSR 断言 SPA 缺失（降级）时 GET /decks/:id/sharing 回退 SSR 共享页：
// 模板与 handler 全部保留，不是应用壳。
func TestSharingRouteFallsBackToSSR(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Sharing fallback deck")
	srv.spa = nil

	rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/sharing", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET sharing fallback = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("fallback returned the SPA shell; the SSR sharing page must be preserved")
	}
	if !strings.Contains(rec.Body.String(), ssrSharingMarker(deck.ID)) {
		t.Errorf("fallback is missing the SSR sharing form: %s", snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), `name="username"`) {
		t.Errorf("fallback is missing the SSR grant input: %s", snippet(rec.Body.String()))
	}
}

// TestSharingMutationsKeepCSRF 断言切壳没有移除写路径，也没有放松 CSRF：
// SSR 表单写（/decks/:id/sharing/grant）与 SPA JSON 写（/api/v1/decks/:id/sharing/grants）
// 缺 token 都 403，带正确 token 才通过。
func TestSharingMutationsKeepCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "CSRF cutover deck")
	_, _, _ = createUserAndLogin(t, srv, db, "csrf-cutover-target")

	ssrPath := "/decks/" + u64str(deck.ID) + "/sharing/grant"
	if rec := postForm(t, srv, ssrPath, url.Values{
		"username": {"csrf-cutover-target"}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("SSR grant without CSRF = %d, want 403", rec.Code)
	}
	if rec := postForm(t, srv, ssrPath, url.Values{
		"csrf_token": {ownerCSRF}, "username": {"csrf-cutover-target"}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Errorf("SSR grant with CSRF = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	apiPath := "/api/v1/decks/" + u64str(deck.ID) + "/sharing/grants"
	if rec := jsonRequest(t, srv, http.MethodPost, apiPath, `{"username":"csrf-cutover-target","role":"editor"}`, ownerCookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("SPA grant without CSRF = %d, want 403", rec.Code)
	}
	if rec := jsonRequest(t, srv, http.MethodPost, apiPath, `{"username":"csrf-cutover-target","role":"editor"}`, ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Errorf("SPA grant with CSRF = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// postAccept 发一个带显式 Accept 的 POST，用来断言 Accept 决定返回 JSON 还是 303 重定向。
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

// cloneResponse 是克隆端点的 JSON 响应体。
type cloneResponse struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// TestCloneAPIAllowsOwnerEditorReader 断言 owner/editor/reader 三种角色都能通过
// POST /api/v1/decks/:id/clone 把卡组克隆到自己名下，响应是新卡组的 JSON {id,name}，
// 且新卡组归属调用者（进度不跟随由 clone_test.go 覆盖）。
func TestCloneAPIAllowsOwnerEditorReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone API source")
	seedBasic(t, db, deck.ID, "Q", "A")
	path := "/api/v1/decks/" + u64str(deck.ID) + "/clone"

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
			if body.ID == 0 || body.ID == deck.ID {
				t.Errorf("clone (%s) id = %d, want a new non-zero id", tc.name, body.ID)
			}
			var cloned store.Deck
			if err := db.First(&cloned, "id = ?", body.ID).Error; err != nil {
				t.Fatalf("load cloned deck: %v", err)
			}
			if cloned.OwnerUserID != tc.userID {
				t.Errorf("clone (%s) owner = %d, want %d", tc.name, cloned.OwnerUserID, tc.userID)
			}
		})
	}
}

// TestCloneAPIRejectsNonReaderAndMissingCSRF 断言克隆 API 的两条拒绝路径：
// 无任何授权的陌生用户 403；缺 CSRF token 403；两者都不产生新卡组。
func TestCloneAPIRejectsNonReaderAndMissingCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone guard source")
	path := "/api/v1/decks/" + u64str(deck.ID) + "/clone"

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
	// 正确 token 下 owner 仍可克隆，证明前两条失败是判权/CSRF 而非路径错误。
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

// TestCloneAcceptHeaderChoosesJSONOrRedirect 断言克隆端点的响应形态由 Accept 决定：
// 带参数的 application/json（客户端可能附 charset）仍返回 201 JSON，浏览器表单形态的 Accept
// 仍走 303 重定向到新卡组——避免 JSON 调用被误判成表单提交后跟随 303 拿到 HTML。
func TestCloneAcceptHeaderChoosesJSONOrRedirect(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Clone accept source")
	path := "/api/v1/decks/" + u64str(deck.ID) + "/clone"

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

// TestShareLinkBrowseStaysSSR 把边界钉死：公开分享浏览 /s/:token 仍是 SSR 页面，
// 没有随共享管理页切流改成应用壳——它的口令/锁定/清洗后卡片内容与登录要求仍由 SSR 保证。
func TestShareLinkBrowseStaysSSR(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Public share SSR deck")
	seedBasic(t, db, deck.ID, "公开正面", "公开背面")

	rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("create share link = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	token := extractShareToken(t, rec.Body.String())

	browse := get(t, srv, "/s/"+token, nil)
	if browse.Code != http.StatusOK {
		t.Fatalf("GET /s/<token> = %d, want 200 (body %s)", browse.Code, snippet(browse.Body.String()))
	}
	if strings.Contains(browse.Body.String(), `<div id="app"></div>`) {
		t.Errorf("public share browse returned the SPA shell; it must stay SSR until full parity")
	}
	if !strings.Contains(browse.Body.String(), "公开正面") {
		t.Errorf("public share browse is missing the sanitized card content: %s", snippet(browse.Body.String()))
	}
	if !strings.Contains(browse.Body.String(), "/login") {
		t.Errorf("public share browse no longer prompts login: %s", snippet(browse.Body.String()))
	}
}
