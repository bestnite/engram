package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 复习页的同源 JSON 端点（卡面渲染、埋藏）与范围校验。
//
// 页面本身只发应用壳（见 page_routes_test.go），数据与写操作全走这里钉住的端点；
// 两个写端点都挂会话 CSRF，且范围参数必须整次校验通过。

type queueBody struct {
	CardID    string `json:"card_id"`
	Remaining int    `json:"remaining"`
	Cards     []struct {
		CardID string `json:"card_id"`
		DeckID string `json:"deck_id"`
	} `json:"cards"`
}

// reviewRenderBody 是卡面渲染响应的对外形态。
type reviewRenderBody struct {
	CardID    string `json:"card_id"`
	FrontHTML string `json:"front_html"`
	BackHTML  string `json:"back_html"`
	EditHref  string `json:"edit_href"`
}

// TestReviewRouteServesShell 断言已登录用户访问 GET /review 得到 SPA 应用壳，由客户端路由渲染复习页。
// 卡组范围参数同样接受：/review?deck=A&deck=B 返回的仍是应用壳。

// TestReviewRouteRejectsInvalidAndUnreadableScope 断言范围判定先于切壳执行：非数字或 0 的
// deck 参数 400；范围里出现读不到的卡组 403。两者都不返回应用壳。
func TestReviewRouteRejectsInvalidAndUnreadableScope(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	foreign := seedReviewDeck(t, db, ownerID, "Foreign review deck")

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"non_numeric", "/review?deck=abc", http.StatusBadRequest},
		{"zero", "/review?deck=0", http.StatusBadRequest},
		{"unreadable", "/review?deck=" + foreign.PublicID, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 用陌生用户访问 owner 的私有卡组：范围校验必须整次失败。
			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "review-scope-stranger-"+tc.name)
			rec := getWithCookies(t, srv, tc.path, strangerCookies)
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d (body %s)", tc.path, rec.Code, tc.want, snippet(rec.Body.String()))
			}
			if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
				t.Errorf("GET %s returned the SPA shell on a denied scope", tc.path)
			}
		})
	}

	// 合法范围仍返回应用壳，证明前面的失败来自范围校验而非路径错误。
	if rec := getWithCookies(t, srv, "/review?deck="+foreign.PublicID, cookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET review with own deck = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewBuryRequiresCSRFAndReader 断言埋藏端点的拒绝路径：无会话 401、缺/错 CSRF 403、
// 读不到卡组的陌生用户 4xx、卡不在所选范围内 400；任一拒绝都不得写 card_states。
func TestReviewBuryRequiresCSRFAndReader(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Bury guard deck")
	other := seedReviewDeck(t, db, ownerID, "Bury other deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	valid := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}

	// 无会话。
	anon := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, nil, "")
	if anon.Code != http.StatusForbidden {
		t.Fatalf("bury without session = %d, want 403 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	// 缺 CSRF 与错 CSRF。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("bury without CSRF = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, cookies, "not-the-token"); rec.Code != http.StatusForbidden {
		t.Errorf("bury with bad CSRF = %d, want 403", rec.Code)
	}
	// 卡不在所选范围内。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{"card_id": cardPub, "deck": []string{other.PublicID}}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("bury out-of-scope card = %d, want 400", rec.Code)
	}
	// 陌生用户即使拿到卡片 id 也读不到卡组。
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "bury-outsider")
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, outsiderCookies, outsiderCSRF); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("bury by outsider = %d, want 4xx", rec.Code)
	}

	var states int64
	db.Model(&store.CardState{}).Count(&states)
	if states != 0 {
		t.Fatalf("denied bury wrote %d card_states rows, want 0", states)
	}
}

// TestReviewBuryDefersCardAndKeepsScope 断言埋藏的核心行为：reader 及以上可埋藏本人进度，
// 埋藏把到期日推到下一个复习日（当天队列少一张），响应带同范围重建后的队列，且跨卡组范围不退化。
func TestReviewBuryDefersCardAndKeepsScope(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Bury deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Bury deck B")
	noteA := seedBasic(t, db, deckA.ID, "A front", "A back")
	seedBasic(t, db, deckB.ID, "B front", "B back")
	cardA := cardIDOfNote(t, db, noteA.ID)
	cardAPub := cardPublicIDOfNote(t, db, noteA.ID)

	// 多卡组范围：埋藏 A 卡后队列应只剩 B 卡，范围仍是 A∪B。
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{
		"card_id": cardAPub, "deck": []string{deckA.PublicID, deckB.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("bury = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body queueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bury response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.Remaining != 1 || len(body.Cards) != 1 {
		t.Fatalf("after bury remaining=%d cards=%d, want 1/1 (body %s)", body.Remaining, len(body.Cards), snippet(rec.Body.String()))
	}
	if body.Cards[0].DeckID != deckB.PublicID {
		t.Errorf("after bury the surviving card is from deck %s, want deck B %s", body.Cards[0].DeckID, deckB.PublicID)
	}

	// 埋藏把到期日推到下一个复习日：该卡的状态行 due_at 必须晚于现在。
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardA, ownerID).First(&st).Error; err != nil {
		t.Fatalf("load buried card state: %v", err)
	}
	if st.DueAt == nil || !st.DueAt.After(time.Now().UTC()) {
		t.Fatalf("buried due_at = %v, want a future timestamp", st.DueAt)
	}

	// 同范围再取一次队列，仍只剩 B 卡（范围未退化）。
	due := getWithCookies(t, srv, "/api/v1/review/due?deck="+deckA.PublicID+"&deck="+deckB.PublicID, cookies)
	if due.Code != http.StatusOK {
		t.Fatalf("GET due after bury = %d, want 200 (body %s)", due.Code, snippet(due.Body.String()))
	}
	var dueBody struct {
		Cards []struct {
			DeckID string `json:"deck_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(due.Body.Bytes(), &dueBody); err != nil {
		t.Fatal(err)
	}
	if len(dueBody.Cards) != 1 || dueBody.Cards[0].DeckID != deckB.PublicID {
		t.Errorf("due after bury = %+v, want only deck B %s", dueBody.Cards, deckB.PublicID)
	}
}

// TestReviewRenderReturnsSanitizedHTML 断言卡面渲染端点返回服务端清洗后的 HTML：脚本与
// 事件属性被去掉、合法内容保留、edit_href 指向卡片编辑页；缺会话/CSRF 与越权范围都被拒绝。
func TestReviewRenderReturnsSanitizedHTML(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Render deck")
	note := seedBasic(t, db, deck.ID,
		"<script>alert(1)</script> front-text",
		"<img src=x onerror=alert(1)> back-text")
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	// 拒绝路径。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without session = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without CSRF = %d, want 403", rec.Code)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body reviewRenderBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.FrontHTML == "" || body.BackHTML == "" {
		t.Fatalf("render returned empty HTML: %+v", body)
	}
	for name, frag := range map[string]string{"front": body.FrontHTML, "back": body.BackHTML} {
		for _, bad := range []string{"<script", "onerror", "onclick"} {
			if strings.Contains(frag, bad) {
				t.Errorf("render %s HTML still contains %q: %s", name, bad, frag)
			}
		}
	}
	if !strings.Contains(body.FrontHTML, "front-text") || !strings.Contains(body.BackHTML, "back-text") {
		t.Errorf("render dropped benign content: %+v", body)
	}
	if body.EditHref != "/decks/"+deck.PublicID+"/notes/"+note.PublicID {
		t.Errorf("render edit_href = %q, want the note edit path", body.EditHref)
	}
}

// TestReviewSuspendRemovesCardForCallerOnly 覆盖复习页的暂停端点：缺 CSRF 被拒且不写库（反面）；
// 暂停后响应里的队列不再有这张卡，状态行带 suspended_at；进度数值不变。
func TestReviewSuspendRemovesCardForCallerOnly(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Suspend deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	seedBasic(t, db, deck.ID, "Q2", "A2")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	req := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}

	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/suspend", req, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("suspend without CSRF = %d, want 403", rec.Code)
	}
	var states int64
	db.Model(&store.CardState{}).Count(&states)
	if states != 0 {
		t.Fatalf("denied suspend wrote %d card_states rows, want 0", states)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/suspend", req, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body queueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode suspend response: %v", err)
	}
	for _, c := range body.Cards {
		if c.CardID == cardPub {
			t.Fatalf("suspended card %s is still in the returned queue", cardPub)
		}
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&st).Error; err != nil {
		t.Fatalf("load suspended state: %v", err)
	}
	if st.SuspendedAt == nil || st.State != "new" {
		t.Errorf("state after suspend = %+v, want suspended_at set on a new-card row", st)
	}
}

// TestReviewBuryUsesTheUsersReviewDay 断言埋藏推到「用户自己的」下一个复习日起点：到期时刻在用户
// 时区里正好是日切点（默认 04:00）。反面：按 UTC 计算时，UTC+14 的用户会得到本地 18:00。
func TestReviewBuryUsesTheUsersReviewDay(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	const tz = "Pacific/Kiritimati"
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("timezone", tz).Error; err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	deck := seedReviewDeck(t, db, ownerID, "Bury tz deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("bury = %d (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardIDOfNote(t, db, note.ID), ownerID).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	local := st.DueAt.In(loc)
	if local.Hour() != store.DefaultDayCutoffHour || local.Minute() != 0 {
		t.Errorf("buried until %s local, want the next %02d:00 in %s", local.Format(time.RFC3339), store.DefaultDayCutoffHour, tz)
	}
}
