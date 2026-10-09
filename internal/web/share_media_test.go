package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 L3：「登录用户通过分享链接打开的卡组计入其可见集合，媒体读取据此放行」。
//
// 打开动作走 /api/v1/share/:token（GET 或 POST .../unlock），它登记会话级媒体授权并返回清洗后的内容；
// 浏览器随后对图片发起的 GET /media/<sha256> 才放行。语义（本文件钉死）：
//   - 已登录但无授权的访客，未打开过分享链接 → 该卡组的媒体 404；
//   - 打开成功（无口令链接，或口令正确）之后 → 该卡组被引用的媒体 200；
//   - 口令错的链接不登记授权；被撤销 / 已过期的链接即使之前打开过，也不得再拿到媒体；
//   - 只覆盖被分享的那个卡组：别的卡组的媒体、没有任何 note 引用的媒体仍 404；
//   - 授权挂在**服务端会话**上：另一个（同属一个用户）的会话也拿不到。
//
// 夹具一律经真实写入路径构造（NoteStore 写 note、HTTP 上传媒体），保证测的是生产链路。

// visitorSessionID 取某用户最近一条会话的 id（测试里用来断言/定位分享授权行）。
func visitorSessionID(t *testing.T, db *gorm.DB, userID uint64) string {
	t.Helper()
	var sess store.Session
	if err := db.Where("user_id = ?", userID).Order("created_at desc, id desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row for user %d: %v", userID, err)
	}
	return sess.ID
}

// shareGrantRows 返回 (session_id, deck_id) 的分享授权行数。
func shareGrantRows(t *testing.T, db *gorm.DB, sessionID string, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Table("share_session_decks").
		Where("session_id = ? AND deck_id = ?", sessionID, deckID).Count(&n).Error; err != nil {
		t.Fatalf("count share_session_decks: %v", err)
	}
	return n
}

// seedReferencedMediaDeck 建一个 owner 的卡组，上传一份媒体，并让卡组里的一条 note 引用它；
// tag 让不同卡组拿到不同 sha256（内容寻址，字节不同即 sha 不同）。
func seedReferencedMediaDeck(t *testing.T, srv *Server, db *gorm.DB, ownerID uint64, ownerCookies []*http.Cookie, ownerCSRF, name string, tag byte) (*store.Deck, string) {
	t.Helper()
	deck := seedDeck(t, db, ownerID, name)
	sha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, append(pngBody(), tag))
	referenceMediaBy(t, db, deck.ID, &ownerID, sha)
	return deck, sha
}

// createShareLinkForDeck 建一条分享链接并取回明文 token；password 为空 = 无口令。
// 兼容既有测试的调用形态，内部走 SPA 的 JSON 端点。
func createShareLinkForDeck(t *testing.T, srv *Server, deckPublicID string, ownerCookies []*http.Cookie, ownerCSRF, password string) string {
	t.Helper()
	return createShareLinkJSON(t, srv, deckPublicID, ownerCookies, ownerCSRF, password)
}

// openShareLink 以访客身份打开一条无口令分享链接（GET /api/v1/share/:token），登记媒体授权。
func openShareLink(t *testing.T, srv *Server, token string, cookies []*http.Cookie) {
	t.Helper()
	rec := getWithCookies(t, srv, "/api/v1/share/"+token, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/share/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestShareLinkMediaReadableAfterBrowse 是 L3 的主验收（无口令链接）：
// 未打开过分享链接的登录访客读不到私有卡组的媒体；打开成功后同一会话即可读到。
func TestShareLinkMediaReadableAfterBrowse(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 open deck", 'a')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-open")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	// 1) 未打开过分享链接：无授权 → 404。
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before browse: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) 打开无口令分享链接成功 → 授权登记。
	openShareLink(t, srv, token, visitorCookies)

	// 3) 授权登记在访客自己的会话上，媒体即可读。
	sessionID := visitorSessionID(t, db, visitorID)
	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 1 {
		t.Errorf("share_session_decks rows = %d, want 1", n)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after browse: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaGrantExpiryIsMinOfLinkAndSession 断言授权行的过期时刻取「链接过期」与「会话过期」较早者。
func TestShareLinkMediaGrantExpiryIsMinOfLinkAndSession(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, _ := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 expiry deck", 'b')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-expiry")

	linkExp := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	plaintext, _, err := store.NewShareLinkStore(db).Create(t.Context(), store.ShareLinkInput{
		DeckID: deck.ID, ExpiresAt: &linkExp, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("seed share link: %v", err)
	}
	openShareLink(t, srv, plaintext, visitorCookies)

	var row struct {
		ExpiresAt time.Time
	}
	if err := db.Table("share_session_decks").
		Select("expires_at").
		Where("session_id = ? AND deck_id = ?", visitorSessionID(t, db, visitorID), deck.ID).
		Scan(&row).Error; err != nil {
		t.Fatalf("load share_session_decks row: %v", err)
	}
	if !row.ExpiresAt.Equal(linkExp) {
		t.Errorf("grant expires_at = %s, want link expiry %s (min of link and session)", row.ExpiresAt, linkExp)
	}
}

// TestShareLinkPasswordGateForMedia 覆盖口令保护的链接：未 unlock 404、口令错 404、口令对 200。
func TestShareLinkPasswordGateForMedia(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 locked deck", 'c')
	visitorID, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-locked")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "letmein99")
	sessionID := visitorSessionID(t, db, visitorID)

	// 1) 未解锁、未授权 → 404。
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before unlock: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) 口令错误 → 401，且不登记授权 → 404。
	bad := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"wrong-pass"}`, visitorCookies, "")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if n := shareGrantRows(t, db, sessionID, deck.ID); n != 0 {
		t.Errorf("wrong password recorded %d share grants, want 0", n)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after wrong password: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 3) 口令正确 → 200，授权随之登记。
	good := jsonRequest(t, srv, http.MethodPost, "/api/v1/share/"+token+"/unlock", `{"password":"letmein99"}`, visitorCookies, "")
	if good.Code != http.StatusOK {
		t.Fatalf("correct password status = %d, want 200 (body %s)", good.Code, snippet(good.Body.String()))
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after unlock: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaLapsesAfterRevoke 覆盖撤销：之前打开过（已登记授权）的会话，链接被撤销后媒体必须立即 404。
func TestShareLinkMediaLapsesAfterRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 revoke deck", 'd')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-revoke")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("visitor GET %s before revoke = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	var link store.ShareLink
	if err := db.Where("deck_id = ?", deck.ID).First(&link).Error; err != nil {
		t.Fatalf("load share link: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodDelete,
		shareLinkJSONPath(deck.PublicID)+"/revoke/"+link.Token, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("revoke share link status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaLapsesAfterLinkExpiry 覆盖过期：链接在打开时有效、之后过期，已登记的授权不得再放行。
func TestShareLinkMediaLapsesAfterLinkExpiry(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 expiry lapse deck", 'e')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-lapse")
	target := "/media/" + sha

	linkExp := time.Now().UTC().Add(time.Hour)
	plaintext, _, err := store.NewShareLinkStore(db).Create(t.Context(), store.ShareLinkInput{
		DeckID: deck.ID, ExpiresAt: &linkExp, CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("seed share link: %v", err)
	}
	openShareLink(t, srv, plaintext, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("visitor GET %s before expiry = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 让链接过期（真库里把 expires_at 推到过去，等价于时间前进）。
	if err := db.Model(&store.ShareLink{}).Where("deck_id = ?", deck.ID).
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatalf("expire share link: %v", err)
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after link expiry: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaCoversOnlyTheSharedDeck 覆盖范围：授权只覆盖被分享的卡组里的引用。
// 访客打开 A 的分享链接后，B 卡组的媒体（同属 owner）与没有任何 note 引用的媒体都必须 404。
func TestShareLinkMediaCoversOnlyTheSharedDeck(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deckA, shaA := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 scope A", 'f')
	_, shaB := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 scope B", 'g')
	// 一份没有任何 note 引用的媒体（owner 上传、无人引用）。
	unreferenced := uploadAndSha(t, srv, ownerCookies, ownerCSRF, append(pngBody(), 'h'))

	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-scope")
	token := createShareLinkJSON(t, srv, deckA.PublicID, ownerCookies, ownerCSRF, "")
	openShareLink(t, srv, token, visitorCookies)

	if rec := getWithCookies(t, srv, "/media/"+shaA, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("shared deck media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, "/media/"+shaB, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("other deck media = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, "/media/"+unreferenced, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unreferenced media = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestShareLinkMediaGrantIsSessionScoped 覆盖会话隔离：授权挂在打开分享链接的那个会话上，
// 另一个（同属一个用户、未打开过）的会话拿不到媒体。
func TestShareLinkMediaGrantIsSessionScoped(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 session scope", 'i')
	_, visitorCookies, _ := createUserAndLogin(t, srv, db, "l3-visitor-session")
	target := "/media/" + sha
	token := createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "")

	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("opening session GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 另一个账号的会话（从未打开分享链接）仍 404。
	_, otherCookies, _ := createUserAndLogin(t, srv, db, "l3-other-session")
	if rec := getWithCookies(t, srv, target, otherCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unrelated session GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestShareLinkJoinGrantsReaderAccess 钉死「打开链接只读、入伙要点按钮」的语义：
//   - 打开链接：媒体放行（会话级只读），但卡组**不进**访客的列表，也不写任何授权行；
//   - POST /api/v1/share/:token/join：拿到显式 reader 授权，卡组进入他的列表；
//   - 属主自己按 join 幂等且不产生授权行；未登录按 join 被会话/CSRF 门拒（403）；
//   - 从未打开过链接的第三个账号读不到媒体；匿名访客仍可直接浏览 /s/:token。
func TestShareLinkJoinGrantsReaderAccess(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck, sha := seedReferencedMediaDeck(t, srv, db, ownerID, ownerCookies, ownerCSRF, "L3 join deck", 'j')
	visitorID, visitorCookies, visitorCSRF := createUserAndLogin(t, srv, db, "l3-visitor-join")
	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "l3-stranger-join")
	target := "/media/" + sha
	joinPath := "/api/v1/share/" + createShareLinkJSON(t, srv, deck.PublicID, ownerCookies, ownerCSRF, "") + "/join"
	token := strings.TrimSuffix(strings.TrimPrefix(joinPath, "/api/v1/share/"), "/join")

	// 1) 打开链接前：列表里没有它，媒体 404。
	if body := deckListingBody(t, srv, visitorCookies); strings.Contains(body, deck.Name) {
		t.Fatalf("deck appeared in the visitor's listing before anything was opened: %s", snippet(body))
	}
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("before browse: visitor GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) 打开链接：只读浏览 + 会话级媒体放行；但「打开一次」不等于入伙。
	openShareLink(t, srv, token, visitorCookies)
	if rec := getWithCookies(t, srv, target, visitorCookies); rec.Code != http.StatusOK {
		t.Fatalf("after browse: visitor GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}
	if body := deckListingBody(t, srv, visitorCookies); strings.Contains(body, deck.Name) {
		t.Fatalf("opening the link must not add the deck to the visitor's listing: %s", snippet(body))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, visitorID); err != nil || role != "" {
		t.Fatalf("opening the link wrote a grant row: role=%q err=%v, want none", role, err)
	}

	// 3) 入伙：显式 join 才写授权，卡组随后进入列表。
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", visitorCookies, visitorCSRF); rec.Code != http.StatusOK {
		t.Fatalf("join status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if body := deckListingBody(t, srv, visitorCookies); !strings.Contains(body, deck.Name) {
		t.Fatalf("deck missing from the visitor's listing after joining: %s", snippet(body))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, visitorID); err != nil || role != store.RoleReader {
		t.Fatalf("grant role after join = %q err=%v, want %q", role, err, store.RoleReader)
	}

	// 4) 属主自己按 join：幂等成功，不产生授权行（owner 列已是唯一真相）。
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", ownerCookies, ownerCSRF); rec.Code != http.StatusOK {
		t.Fatalf("owner join status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, ownerID); err != nil || role != "" {
		t.Fatalf("owner got a grant row from joining their own deck: role=%q err=%v, want none", role, err)
	}

	// 5) 未登录按 join：会话/CSRF 门先拒，且不留下任何授权行。
	if rec := jsonRequest(t, srv, http.MethodPost, joinPath, "", nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous join status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 6) 从未打开过链接的第三个账号：媒体仍 404。
	if rec := getWithCookies(t, srv, target, strangerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("unrelated user GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 7) 匿名仍可直接浏览分享链接。
	if rec := get(t, srv, "/s/"+token, nil); rec.Code != http.StatusOK {
		t.Fatalf("anonymous GET /s/<token> = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}
