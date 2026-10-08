package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckCloneCopiesContentWithoutProgress 是 M5-4 的主验收：
// 克隆后 note/card 数量一致、新 owner 名下 card_states 为 0、原卡组不受影响。
func TestDeckCloneCopiesContentWithoutProgress(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Source deck")
	// 三种题型：basic(1 张)、basic_both(2 张)、cloze(2 张) —— 覆盖一 note 多 card。
	seedBasic(t, db, deck.ID, "Q1", "A1", "tag-one")
	both := &store.Note{DeckID: deck.ID, Kind: "basic_both"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), both, map[string]any{"front": "Q2", "back": "A2"}); err != nil {
		t.Fatalf("create basic_both: %v", err)
	}
	cloze := &store.Note{DeckID: deck.ID, Kind: "cloze"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), cloze, map[string]any{"text": "{{c1::one}} and {{c2::two}}"}); err != nil {
		t.Fatalf("create cloze: %v", err)
	}

	srcNotes := countNotes(t, db, deck.ID)
	srcCards := countCards(t, db, deck.ID)
	if srcNotes != 3 || srcCards != 5 {
		t.Fatalf("seed produced (%d notes, %d cards), want (3, 5)", srcNotes, srcCards)
	}

	// owner 克隆自己的卡组。
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/clone", url.Values{"csrf_token": {ownerCSRF}}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckIDFromLocation(t, rec.Header().Get("Location"))

	// 新卡组归属调用者、名字带副本后缀、预设另建一份、默认私有。
	var cloned store.Deck
	if err := db.First(&cloned, "id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != ownerID {
		t.Errorf("cloned deck owner = %d, want %d", cloned.OwnerUserID, ownerID)
	}
	if cloned.ID == deck.ID {
		t.Fatal("clone reused the source deck id")
	}
	if cloned.PresetID == deck.PresetID {
		t.Errorf("clone shares the source preset id %d, want a copied preset", cloned.PresetID)
	}
	if cloned.Name == deck.Name || !strings.Contains(cloned.Name, deck.Name) {
		t.Errorf("cloned deck name = %q, want it to contain %q", cloned.Name, deck.Name)
	}

	// 内容数量一致。
	if got := countNotes(t, db, newID); got != srcNotes {
		t.Errorf("cloned note count = %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, newID); got != srcCards {
		t.Errorf("cloned card count = %d, want %d", got, srcCards)
	}

	// 进度为零：新 owner 在新 card 上没有 card_states 行。
	var states int64
	if err := db.Model(&store.CardState{}).
		Where("user_id = ? AND card_id IN (?)", ownerID, cardIDSubquery(db, newID)).
		Count(&states).Error; err != nil {
		t.Fatalf("count cloned card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("cloned deck carries progress: card_states rows = %d, want 0", states)
	}

	// 原卡组不受影响：数量不变。
	if got := countNotes(t, db, deck.ID); got != srcNotes {
		t.Errorf("source note count changed to %d, want %d", got, srcNotes)
	}
	if got := countCards(t, db, deck.ID); got != srcCards {
		t.Errorf("source card count changed to %d, want %d", got, srcCards)
	}

	// 克隆也写一行审计。
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckClone)
	if err != nil {
		t.Fatalf("count clone audit: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckClone, n)
	}
}

// TestDeckCloneAllowedForReader 覆盖「自己可读即可克隆」：被授权 reader 能把内容复制到自己名下。
func TestDeckCloneAllowedForReader(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Readable deck")
	seedBasic(t, db, deck.ID, "Q", "A")
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "cloner2")
	grantRole(t, srv, deck.ID, user2ID, store.RoleReader, ownerCookies, ownerCSRF)

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/clone", url.Values{"csrf_token": {u2CSRF}}, u2Cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reader POST clone status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	newID := parseDeckIDFromLocation(t, rec.Header().Get("Location"))
	var cloned store.Deck
	if err := db.First(&cloned, "id = ?", newID).Error; err != nil {
		t.Fatalf("load cloned deck: %v", err)
	}
	if cloned.OwnerUserID != user2ID {
		t.Errorf("cloned deck owner = %d, want reader id %d", cloned.OwnerUserID, user2ID)
	}
	if got := countNotes(t, db, newID); got != 1 {
		t.Errorf("cloned note count = %d, want 1", got)
	}
	var states int64
	if err := db.Model(&store.CardState{}).Where("user_id = ?", user2ID).Count(&states).Error; err != nil {
		t.Fatalf("count reader card_states: %v", err)
	}
	if states != 0 {
		t.Errorf("reader clone carries progress: %d rows, want 0", states)
	}
}

// countNotes 统计卡组下未软删除的 note 数。
func countNotes(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&n).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	return n
}

// countCards 统计卡组下可见的 card 数（join notes 排除软删除内容）。
func countCards(t *testing.T, db *gorm.DB, deckID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Table("cards").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID).
		Count(&n).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	return n
}

// cardIDSubquery 返回某卡组下 card id 的子查询，用于统计 card_states。
func cardIDSubquery(db *gorm.DB, deckID uint64) *gorm.DB {
	return db.Table("cards").
		Select("cards.id").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id = ? AND cards.deleted_at IS NULL", deckID)
}

// parseDeckIDFromLocation 从 /decks/<id>/notes 形式的 Location 中取出卡组 id。
func parseDeckIDFromLocation(t *testing.T, location string) uint64 {
	t.Helper()
	parts := strings.Split(strings.Trim(location, "/"), "/")
	if len(parts) < 2 || parts[0] != "decks" {
		t.Fatalf("unexpected clone redirect Location %q", location)
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || id == 0 {
		t.Fatalf("cannot parse deck id from Location %q: %v", location, err)
	}
	return id
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
