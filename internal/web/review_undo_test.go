package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// undoBody 是撤销端点的对外形态：同范围重建的队列 + 被撤销卡的对外 id。
// undone_card_id 是前端把当前卡定位回去的依据（队列按 due_at 排序，不保证它在首位）。
type undoBody struct {
	UndoneCardID string `json:"undone_card_id"`
	Remaining    int    `json:"remaining"`
	Cards        []struct {
		CardID string `json:"card_id"`
	} `json:"cards"`
}

// submitSelfReview 通过 SPA 自评入口给一张卡评分（写一条 reviews 行并推进 card_states）。
func submitSelfReview(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, cardPub, deckPub string, rating, expectedVersion int) {
	t.Helper()
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPub, "rating": rating, "expected_version": expectedVersion,
		"deck": []string{deckPub},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("self review rating=%d version=%d = %d, want 200 (body %s)",
			rating, expectedVersion, rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewUndoRestoresStateAndDeletesReview 断言撤销的核心行为：删除被撤销的那条 reviews 行、
// card_states 精确恢复到该次评分之前（含到期日）、写一条 review.undo 审计行，且响应显式带
// 被撤销卡的对外 id（undone_card_id）。
func TestReviewUndoRestoresStateAndDeletesReview(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, afterFirst.Version)
	var beforeUndo store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&beforeUndo).Error; err != nil {
		t.Fatalf("load state before undo: %v", err)
	}

	var reviewsBefore int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsBefore)
	if reviewsBefore != 2 {
		t.Fatalf("reviews before undo = %d, want 2", reviewsBefore)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": beforeUndo.Version,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body undoBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode undo response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.UndoneCardID != cardPub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, cardPub)
	}

	// 被撤销的那条 reviews 行被删除，只剩第一次的（append-only 的唯一例外）。
	var reviewsAfter int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsAfter)
	if reviewsAfter != 1 {
		t.Errorf("reviews after undo = %d, want 1", reviewsAfter)
	}

	// card_states 精确恢复到第一次评分之后（即第二次评分之前）的状态。
	var restored store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&restored).Error; err != nil {
		t.Fatalf("load state after undo: %v", err)
	}
	if restored.State != afterFirst.State {
		t.Errorf("state after undo = %q, want %q", restored.State, afterFirst.State)
	}
	if restored.DueAt == nil || afterFirst.DueAt == nil || restored.DueAt.UnixMilli() != afterFirst.DueAt.UnixMilli() {
		t.Errorf("due_at after undo = %v, want %v (exact restore)", restored.DueAt, afterFirst.DueAt)
	}
	if restored.ScheduledDays != afterFirst.ScheduledDays {
		t.Errorf("scheduled_days after undo = %d, want %d", restored.ScheduledDays, afterFirst.ScheduledDays)
	}
	// version 单调推进：撤销自身也要让在途提交失效。
	var afterSecond store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&afterSecond).Error; err != nil {
		t.Fatalf("reload state after undo: %v", err)
	}
	if afterSecond.Version != restored.Version {
		t.Errorf("state version read twice differs: %d vs %d", afterSecond.Version, restored.Version)
	}
	if restored.Version <= afterFirst.Version {
		t.Errorf("version after undo = %d, want > %d (monotonic)", restored.Version, afterFirst.Version)
	}

	// 审计行：撤销写一条 review.undo。
	var audits int64
	db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits)
	if audits != 1 {
		t.Errorf("review.undo audit rows = %d, want 1", audits)
	}
}

// TestReviewUndoReplayIsRejectedWithoutDeletingAnotherReview 是 AUDIT-02 的 HTTP 层验收：
// 同一撤销请求重发第二次必须 409，且只删掉一条评分——网络重试、双开窗口都不会误删另一条历史评分。
func TestReviewUndoReplayIsRejectedWithoutDeletingAnotherReview(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo replay deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var v1 store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&v1).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, v1.Version)
	var v2 store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&v2).Error; err != nil {
		t.Fatalf("load state after second review: %v", err)
	}

	body := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": v2.Version}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", body, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("first undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", body, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replayed undo = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var reviews int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviews)
	if reviews != 1 {
		t.Errorf("reviews after replayed undo = %d, want 1 (must not delete another review)", reviews)
	}
}

// TestReviewUndoReturnsCardToQueue 断言撤销把卡放回队列，且响应里的 undone_card_id 指向它：
// 前端据此定位当前卡，而不是假定它排在重建队列的首位。
func TestReviewUndoReturnsCardToQueue(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo queue deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	submitSelfReview(t, srv, cookies, csrf, cardPub, deck.PublicID, 3, 0)
	var beforeUndo store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardIDOfNote(t, db, note.ID), ownerID).First(&beforeUndo).Error; err != nil {
		t.Fatalf("load state before undo: %v", err)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "expected_version": beforeUndo.Version,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("undo = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body undoBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.UndoneCardID != cardPub {
		t.Errorf("undone_card_id = %q, want %q", body.UndoneCardID, cardPub)
	}
	found := false
	for _, c := range body.Cards {
		if c.CardID == cardPub {
			found = true
		}
	}
	if !found {
		t.Errorf("undone card %s missing from the rebuilt queue: %s", cardPub, snippet(rec.Body.String()))
	}
	if body.Remaining != len(body.Cards) {
		t.Errorf("remaining = %d, want %d (len(cards))", body.Remaining, len(body.Cards))
	}
}

// TestReviewUndoRejections 覆盖撤销端点的拒绝路径：该卡没有复习日志 409、无会话/缺 CSRF 403、
// 卡不在范围内 400、读不到卡组的陌生用户 4xx。任一拒绝都不得删除 reviews 行或写审计。
func TestReviewUndoRejections(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Undo guard deck")
	other := seedReviewDeck(t, db, ownerID, "Undo other deck")
	reviewedNote := seedBasic(t, db, deck.ID, "Reviewed Q", "A")
	bareNote := seedBasic(t, db, deck.ID, "Bare Q", "A")
	reviewedID := cardIDOfNote(t, db, reviewedNote.ID)
	reviewedPub := cardPublicIDOfNote(t, db, reviewedNote.ID)
	barePub := cardPublicIDOfNote(t, db, bareNote.ID)
	submitSelfReview(t, srv, cookies, csrf, reviewedPub, deck.PublicID, 3, 0)
	valid := map[string]any{"card_id": reviewedPub, "deck": []string{deck.PublicID}}

	// ① 该卡没有任何复习日志：409 conflict（schedule.ErrNothingToUndo）。
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": barePub, "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusConflict {
		t.Fatalf("undo without review log = %d, want 409 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// ② 无会话：CSRF 中间件在 handler 之前以 403 csrf_no_session 拒绝，与 bury/render 同口径
	// （handler 内的 401 分支对无会话请求不可达）。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("undo without session = %d, want 403", rec.Code)
	}
	// ③ 缺 CSRF 与错 CSRF：403。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("undo without CSRF = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, cookies, "not-the-token"); rec.Code != http.StatusForbidden {
		t.Errorf("undo with bad CSRF = %d, want 403", rec.Code)
	}
	// ④ 卡不在所选范围内：400（与 bury 同口径）。
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", map[string]any{
		"card_id": reviewedPub, "deck": []string{other.PublicID},
	}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("undo out-of-scope card = %d, want 400", rec.Code)
	}
	// ⑤ 读不到卡组的陌生用户：4xx。
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "undo-outsider")
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/undo", valid, outsiderCookies, outsiderCSRF); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("undo by outsider = %d, want 4xx (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 被拒绝的请求什么都没撤销：被评分的卡仍有恰好一条日志、没有 review.undo 审计、没有新状态行。
	var reviews, audits, states int64
	db.Model(&store.Review{}).Where("card_id = ?", reviewedID).Count(&reviews)
	db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits)
	db.Model(&store.CardState{}).Where("card_id = ?", bareNote.ID).Count(&states)
	if reviews != 1 || audits != 0 || states != 0 {
		t.Fatalf("denied undo changed data: reviews=%d audits=%d bareStates=%d, want 1/0/0", reviews, audits, states)
	}
}
