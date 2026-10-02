package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"example.com/engram/internal/store"
)

// shareTokenPattern 从共享页 HTML 里抓出刚创建的分享链接明文（value="/s/<token>"）。
var shareTokenPattern = regexp.MustCompile(`/s/([A-Za-z0-9_-]{20,})`)

// extractShareToken 从创建成功的共享页响应里取回明文 token。
func extractShareToken(t *testing.T, body string) string {
	t.Helper()
	m := shareTokenPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no share link token found in response body: %s", snippet(body))
	}
	return m[1]
}

func shareLinksPath(deckID uint64) string {
	return "/decks/" + u64str(deckID) + "/sharing/links"
}

// TestShareLinkCreateBrowseThenRevoke 是 M5-3 的主验收：
// 创建后匿名可浏览内容、库里只存摘要、撤销后立即 404。
func TestShareLinkCreateBrowseThenRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Link deck")
	seedBasic(t, db, deck.ID, "秘密正面", "秘密背面")

	rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{
		"csrf_token": {ownerCSRF},
	}, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST create share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	token := extractShareToken(t, rec.Body.String())

	// 库里只存 sha256 摘要，绝不落明文（DESIGN.md §11）。
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

	// 免登录浏览：200 且能看到卡片内容。
	browse := get(t, srv, "/s/"+token, nil)
	if browse.Code != http.StatusOK {
		t.Fatalf("anonymous GET /s/<token> status = %d, want 200 (body %s)", browse.Code, snippet(browse.Body.String()))
	}
	body := browse.Body.String()
	for _, want := range []string{"秘密正面", "秘密背面"} {
		if !strings.Contains(body, want) {
			t.Errorf("browse page missing %q", want)
		}
	}
	// 只读页不得带任何进度信息；复习入口只提示登录。
	if strings.Contains(body, "review.remaining") || strings.Contains(body, "review.done") {
		t.Errorf("browse page leaked review progress markup")
	}
	if !strings.Contains(body, "/login") {
		t.Errorf("browse page does not prompt login for review")
	}

	// 单个撤销后立即 404。
	rec = postForm(t, srv, shareLinksPath(deck.ID)+"/revoke", url.Values{
		"csrf_token": {ownerCSRF}, "link": {link.Token},
	}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST revoke status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := get(t, srv, "/s/"+token, nil).Code; got != http.StatusNotFound {
		t.Errorf("after revoke: GET /s/<token> status = %d, want 404", got)
	}
}

// TestShareLinkRevokeAll 覆盖批量撤销：一次撤销全部后两条链接都 404。
func TestShareLinkRevokeAll(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Bulk deck")
	seedBasic(t, db, deck.ID, "front", "back")

	tokens := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("create link %d status = %d", i, rec.Code)
		}
		tokens = append(tokens, extractShareToken(t, rec.Body.String()))
	}
	rec := postForm(t, srv, shareLinksPath(deck.ID)+"/revoke-all", url.Values{
		"csrf_token": {ownerCSRF},
	}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST revoke-all status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	for _, token := range tokens {
		if got := get(t, srv, "/s/"+token, nil).Code; got != http.StatusNotFound {
			t.Errorf("after revoke-all: GET /s/<token> status = %d, want 404", got)
		}
	}
}

// TestShareLinkPasswordGate 是验收里的“口令错被拒”：错误口令 401 且看不到内容，正确口令放行。
func TestShareLinkPasswordGate(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Locked deck")
	seedBasic(t, db, deck.ID, "口令内容正面", "口令内容背面")

	rec := postForm(t, srv, shareLinksPath(deck.ID), url.Values{
		"csrf_token": {ownerCSRF}, "password": {"letmein99"},
	}, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("create password-protected link status = %d (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	token := extractShareToken(t, rec.Body.String())

	// 未解锁时只渲染口令表单，不泄漏内容。
	form := get(t, srv, "/s/"+token, nil)
	if form.Code != http.StatusOK {
		t.Fatalf("GET password form status = %d, want 200", form.Code)
	}
	if strings.Contains(form.Body.String(), "口令内容正面") {
		t.Errorf("content rendered before the password was supplied")
	}

	// 错误口令：401 且仍然看不到内容。
	bad := postForm(t, srv, "/s/"+token+"/unlock", url.Values{"password": {"wrong-pass"}}, nil)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if strings.Contains(bad.Body.String(), "口令内容正面") {
		t.Errorf("content rendered despite a wrong password")
	}

	// 正确口令：放行。
	good := postForm(t, srv, "/s/"+token+"/unlock", url.Values{"password": {"letmein99"}}, nil)
	if good.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200 (body %s)", good.Code, snippet(good.Body.String()))
	}
	if !strings.Contains(good.Body.String(), "口令内容正面") {
		t.Errorf("content missing after correct password")
	}
}

// TestShareLinkExpiredReturns404 覆盖过期：解析路径把过期与不存在同样处理。
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

// TestShareLinkWriteRequiresOwnerAndCSRF 覆盖反面用例：非 owner 与缺 CSRF 的写请求都被拒且不落库。
func TestShareLinkWriteRequiresOwnerAndCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Guarded deck")
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "linkviewer")
	path := shareLinksPath(deck.ID)

	// 缺 CSRF：403，且没有链接建立。
	if rec := postForm(t, srv, path, url.Values{"password": {"x"}}, ownerCookies); rec.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d, want 403", rec.Code)
	}
	// 非 owner（无授权）：403。
	if rec := postForm(t, srv, path, url.Values{"csrf_token": {u2CSRF}}, u2Cookies); rec.Code != http.StatusForbidden {
		t.Fatalf("create by non-owner status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	links, err := store.NewShareLinkStore(db).ListByDeck(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("list share links: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("share links written despite rejected requests: %d", len(links))
	}
	// 缺 CSRF 的创建请求也没通过。
	_ = ownerCSRF
}
