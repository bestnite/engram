package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件是公开只读分享浏览 SPA 化的验收（internal/web/spa_share.go）。
//
// 覆盖：GET /s/:token 的切壳与 SSR 回退、撤销/未知链接仍 404、JSON 内容复用服务端清洗渲染、
// 口令门禁（解锁前不返回正文）、以及打开成功时经 JSON 登记会话级媒体授权。走真实路由与 SQLite。

// shareResponse 是 GET /api/v1/share/:token 的响应形态（测试解码用）。
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

// TestSPAShareBrowseCutover 覆盖 GET /s/:token 的切壳、撤销/未知链接的 404，以及 SSR 回退。
func TestSPAShareBrowseCutover(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Cutover share deck")
	seedBasic(t, db, deck.ID, "正面", "背面")
	token := createShareLinkForDeck(t, srv, deck.ID, ownerCookies, ownerCSRF, "")

	assertServesSPAShell(t, get(t, srv, "/s/"+token, nil), "GET /s/<token>")

	// 未知链接仍 404（切壳不改变可达性判定）。
	if rec := get(t, srv, "/s/does-not-exist-token-1234567890", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /s/<unknown> = %d, want 404", rec.Code)
	}
}

// TestSPAShareJSONContent 断言 JSON 内容端点返回服务端清洗后的正反面 HTML，且未知链接 404。
func TestSPAShareJSONContent(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "JSON share deck")
	seedBasic(t, db, deck.ID, "安全正面", "安全背面")
	seedBasic(t, db, deck.ID, `<script>alert(1)</script>`, "背面")
	token := createShareLinkForDeck(t, srv, deck.ID, ownerCookies, ownerCSRF, "")

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

// TestSPAShareJSONPasswordGate 断言有口令链接在解锁前不返回正文，错误口令 401、正确口令 200。
func TestSPAShareJSONPasswordGate(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Locked share deck")
	seedBasic(t, db, deck.ID, "机密正面", "机密背面")
	token := createShareLinkForDeck(t, srv, deck.ID, ownerCookies, ownerCSRF, "letmein99")

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

// TestSPAShareJSONRecordsMediaGrant 断言经 JSON 成功打开分享页后，登录访客对同一会话即可读媒体
// （与 SSR 的 recordShareGrant 同源，媒体授权不因切壳而丢失）。
func TestSPAShareJSONRecordsMediaGrant(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "JSON media deck", 'a')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "spa-json-visitor")
	token := createShareLinkForDeck(t, srv, deck.ID, ownerCookies, ownerCSRF, "")
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
