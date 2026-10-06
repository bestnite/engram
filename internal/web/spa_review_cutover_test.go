package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 GET /review 的 SPA 规范路径切流与复习页新增的两个会话 CSRF 端点（DESIGN.md
// §6.1、§8.1、§8.2、§8.5）：
//
//   - GET /review 在 SPA 已加载时返回应用壳，由客户端路由渲染复习页；会话与卡组范围判定
//     先于切壳执行——匿名 303 登录页、非法 deck 参数 400、范围里读不到的卡组 403，都不因
//     返回应用壳而放行。SPA 缺失（降级）时回退 SSR 复习页，旧 handler、模板与脚本全部保留。
//   - POST /api/v1/review/bury：埋藏只写本人进度（reader 即可），会话 + CSRF 保护，响应带
//     同范围重建后的队列。
//   - POST /api/v1/review/render：只返回服务端清洗后的卡面 HTML（唯一 HTML 汇），会话 + CSRF
//     保护；SPA 绝不把 fields 原文送进 {@html}。
//
// SSR 的评分/动作写路径（/review/answer、/review/action）未改动，仍由各自既有测试覆盖。

// spaQueueBody 是复习队列响应的对外形态（埋藏与渲染入口共用的最小字段）。
type spaQueueBody struct {
	CardID    uint64 `json:"card_id"`
	Remaining int    `json:"remaining"`
	Cards     []struct {
		CardID uint64 `json:"card_id"`
		DeckID uint64 `json:"deck_id"`
	} `json:"cards"`
}

// spaRenderBody 是卡面渲染响应的对外形态。
type spaRenderBody struct {
	CardID    uint64 `json:"card_id"`
	FrontHTML string `json:"front_html"`
	BackHTML  string `json:"back_html"`
	EditHref  string `json:"edit_href"`
}

// TestReviewRouteServesSPAShell 断言已登录用户访问 GET /review 得到 SPA 应用壳，由客户端路由
// 渲染复习页，不再渲染 SSR 复习页（SSR 的 #review-area 与四档按钮必须消失）。
// 卡组范围参数同样接受：/review?deck=A&deck=B 返回的仍是应用壳。
func TestReviewRouteServesSPAShell(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Review shell deck")
	seedBasic(t, db, deck.ID, "Q", "A")

	for _, path := range []string{
		"/review",
		"/review?deck=" + u64str(deck.ID),
		"/review?deck=" + u64str(deck.ID) + "&deck=" + u64str(deck.ID),
	} {
		rec := getWithCookies(t, srv, path, cookies)
		assertSPAShell(t, rec)
		if strings.Contains(rec.Body.String(), `id="review-area"`) {
			t.Errorf("GET %s still renders the SSR review page: %s", path, snippet(rec.Body.String()))
		}
	}
}

// TestReviewRouteRedirectsAnonymous 断言切壳不改动授权：匿名访问 GET /review 仍 303 重定向
// 登录页，应用壳不会泄漏给未登录访客。
func TestReviewRouteRedirectsAnonymous(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/review", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous GET /review = %d loc %q, want 303 /login", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("anonymous GET /review leaked the SPA shell")
	}
}

// TestReviewRouteRejectsInvalidAndUnreadableScope 断言范围判定先于切壳执行：非数字或 0 的
// deck 参数 400；范围里出现读不到的卡组 403。两者都不返回应用壳（DESIGN.md §8.2）。
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
		{"unreadable", "/review?deck=" + u64str(foreign.ID), http.StatusForbidden},
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
	if rec := getWithCookies(t, srv, "/review?deck="+u64str(foreign.ID), cookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET review with own deck = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewRouteFallsBackToSSR 断言 SPA 缺失（降级）时 GET /review 回退 SSR 复习页：
// #review-area 与卡组隐藏字段仍在，不是应用壳。
func TestReviewRouteFallsBackToSSR(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Review fallback deck")
	seedBasic(t, db, deck.ID, "FallbackFront", "FallbackBack")
	srv.spa = nil

	rec := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET review fallback = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
		t.Errorf("fallback returned the SPA shell; the SSR review page must be preserved")
	}
	if !strings.Contains(rec.Body.String(), `id="review-area"`) {
		t.Errorf("fallback is missing the SSR review area: %s", snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), `name="deck" value="`+u64str(deck.ID)+`"`) {
		t.Errorf("fallback is missing the hidden deck field: %s", snippet(rec.Body.String()))
	}
}

// TestSPAReviewBuryRequiresCSRFAndReader 断言埋藏端点的拒绝路径：无会话 401、缺/错 CSRF 403、
// 读不到卡组的陌生用户 4xx、卡不在所选范围内 400；任一拒绝都不得写 card_states。
func TestSPAReviewBuryRequiresCSRFAndReader(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Bury guard deck")
	other := seedReviewDeck(t, db, ownerID, "Bury other deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	valid := map[string]any{"card_id": cardID, "deck": []uint64{deck.ID}}

	// 无会话。
	anon := postSPAJSON(t, srv, "/api/v1/review/bury", valid, nil, "")
	if anon.Code != http.StatusForbidden {
		t.Fatalf("bury without session = %d, want 403 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	// 缺 CSRF 与错 CSRF。
	if rec := postSPAJSON(t, srv, "/api/v1/review/bury", valid, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("bury without CSRF = %d, want 403", rec.Code)
	}
	if rec := postSPAJSON(t, srv, "/api/v1/review/bury", valid, cookies, "not-the-token"); rec.Code != http.StatusForbidden {
		t.Errorf("bury with bad CSRF = %d, want 403", rec.Code)
	}
	// 卡不在所选范围内。
	if rec := postSPAJSON(t, srv, "/api/v1/review/bury", map[string]any{"card_id": cardID, "deck": []uint64{other.ID}}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("bury out-of-scope card = %d, want 400", rec.Code)
	}
	// 陌生用户即使拿到卡片 id 也读不到卡组。
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "bury-outsider")
	if rec := postSPAJSON(t, srv, "/api/v1/review/bury", valid, outsiderCookies, outsiderCSRF); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("bury by outsider = %d, want 4xx", rec.Code)
	}

	var states int64
	db.Model(&store.CardState{}).Count(&states)
	if states != 0 {
		t.Fatalf("denied bury wrote %d card_states rows, want 0", states)
	}
}

// TestSPAReviewBuryDefersCardAndKeepsScope 断言埋藏的核心行为：reader 及以上可埋藏本人进度，
// 埋藏把到期日推到下一个复习日（当天队列少一张），响应带同范围重建后的队列，且跨卡组范围不退化。
func TestSPAReviewBuryDefersCardAndKeepsScope(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Bury deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Bury deck B")
	noteA := seedBasic(t, db, deckA.ID, "A front", "A back")
	seedBasic(t, db, deckB.ID, "B front", "B back")
	cardA := cardIDOfNote(t, db, noteA.ID)

	// 多卡组范围：埋藏 A 卡后队列应只剩 B 卡，范围仍是 A∪B。
	rec := postSPAJSON(t, srv, "/api/v1/review/bury", map[string]any{
		"card_id": cardA, "deck": []uint64{deckA.ID, deckB.ID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("bury = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body spaQueueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bury response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.Remaining != 1 || len(body.Cards) != 1 {
		t.Fatalf("after bury remaining=%d cards=%d, want 1/1 (body %s)", body.Remaining, len(body.Cards), snippet(rec.Body.String()))
	}
	if body.Cards[0].DeckID != deckB.ID {
		t.Errorf("after bury the surviving card is from deck %d, want deck B %d", body.Cards[0].DeckID, deckB.ID)
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
	due := getWithCookies(t, srv, "/api/v1/review/due?deck="+u64str(deckA.ID)+"&deck="+u64str(deckB.ID), cookies)
	if due.Code != http.StatusOK {
		t.Fatalf("GET due after bury = %d, want 200 (body %s)", due.Code, snippet(due.Body.String()))
	}
	var dueBody struct {
		Cards []struct {
			DeckID uint64 `json:"deck_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(due.Body.Bytes(), &dueBody); err != nil {
		t.Fatal(err)
	}
	if len(dueBody.Cards) != 1 || dueBody.Cards[0].DeckID != deckB.ID {
		t.Errorf("due after bury = %+v, want only deck B %d", dueBody.Cards, deckB.ID)
	}
}

// TestSPAReviewRenderReturnsSanitizedHTML 断言卡面渲染端点返回服务端清洗后的 HTML：脚本与
// 事件属性被去掉、合法内容保留、edit_href 指向卡片编辑页；缺会话/CSRF 与越权范围都被拒绝。
func TestSPAReviewRenderReturnsSanitizedHTML(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Render deck")
	note := seedBasic(t, db, deck.ID,
		"<script>alert(1)</script> front-text",
		"<img src=x onerror=alert(1)> back-text")
	cardID := cardIDOfNote(t, db, note.ID)

	// 拒绝路径。
	if rec := postSPAJSON(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardID}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without session = %d, want 403", rec.Code)
	}
	if rec := postSPAJSON(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardID}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without CSRF = %d, want 403", rec.Code)
	}

	rec := postSPAJSON(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardID, "deck": []uint64{deck.ID}}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body spaRenderBody
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
	if body.EditHref != "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID) {
		t.Errorf("render edit_href = %q, want the note edit path", body.EditHref)
	}
}
