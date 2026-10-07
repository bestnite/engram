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

// 分享链接的正规写入口是 SPA 的 JSON 端点（/api/v1/decks/:id/sharing/links），
// 页面层只保留 GET /s/:token 的应用壳与 api/v1/share/:token 的只读内容。

// shareLinkJSONPath 是分享链接的 JSON 增删路径。
func shareLinkJSONPath(deckID uint64) string {
	return "/api/v1/decks/" + u64str(deckID) + "/sharing/links"
}

// createShareLinkJSON 经 SPA JSON 端点建一条分享链接并取回明文 token；password 为空 = 无口令。
func createShareLinkJSON(t *testing.T, srv *Server, deckID uint64, cookies []*http.Cookie, csrf, password string) string {
	t.Helper()
	body := "{}"
	if password != "" {
		body = `{"password":` + strconv.Quote(password) + `}`
	}
	rec := jsonRequest(t, srv, http.MethodPost, shareLinkJSONPath(deckID), body, cookies, csrf)
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

// shareResponse 是 GET /api/v1/share/:token 的对外形态。
type shareResponse struct {
	DeckName         string `json:"deck_name"`
	PasswordRequired bool   `json:"password_required"`
	Notes            []struct {
		FrontHTML string `json:"front_html"`
		BackHTML  string `json:"back_html"`
	} `json:"notes"`
}

// TestShareLinkCreateBrowseThenRevoke 是 M5-3 的主验收：
// 创建后任何人可浏览内容、库里只存摘要、撤销后立即 404。
func TestShareLinkCreateBrowseThenRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Link deck")
	seedBasic(t, db, deck.ID, "秘密正面", "秘密背面")

	token := createShareLinkJSON(t, srv, deck.ID, ownerCookies, ownerCSRF, "")

	// 库里只存 sha256 摘要，绝不落明文。
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

	// 免登录浏览：/s/:token 返回应用壳，内容由 JSON 端点给出。
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
	// 只读内容不得带任何进度信息。
	if strings.Contains(joined, "review.remaining") || strings.Contains(joined, "review.done") {
		t.Errorf("share content leaked review progress markup")
	}

	// 单个撤销后立即 404。
	if rec := jsonRequest(t, srv, http.MethodDelete,
		shareLinkJSONPath(deck.ID)+"/revoke/"+link.Token, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
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
		tokens = append(tokens, createShareLinkJSON(t, srv, deck.ID, ownerCookies, ownerCSRF, ""))
	}
	if rec := jsonRequest(t, srv, http.MethodDelete, shareLinkJSONPath(deck.ID), "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke-all status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
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

	token := createShareLinkJSON(t, srv, deck.ID, ownerCookies, ownerCSRF, "letmein99")

	// 未解锁时只回 password_required，不泄漏内容。
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

	// 错误口令：401 且仍然看不到内容。
	bad := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"wrong-pass"}`, nil, "")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if strings.Contains(bad.Body.String(), "口令内容正面") {
		t.Errorf("content rendered despite a wrong password")
	}

	// 正确口令：返回内容。
	good := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"letmein99"}`, nil, "")
	if good.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200 (body %s)", good.Code, snippet(good.Body.String()))
	}
	if !strings.Contains(good.Body.String(), "口令内容正面") {
		t.Errorf("content missing after correct password: %s", snippet(good.Body.String()))
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
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Guarded deck")
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "linkviewer")
	path := shareLinkJSONPath(deck.ID)

	// 缺 CSRF：403，且没有链接建立。
	if rec := jsonRequest(t, srv, http.MethodPost, path, `{"password":"x"}`, ownerCookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("create without CSRF status = %d, want 403", rec.Code)
	}
	// 非 owner（无授权）：403。
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
