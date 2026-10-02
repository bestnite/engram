package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewRevealedGradedCardCountsAsAgain 是"揭示答案即放弃作答"的验收（DESIGN.md §8.2）：
// 作答类题型一旦揭示答案，再提交必须记 Again（0 分），而不是走判分器拿 Good/Easy。
//
// 页面侧的配合是 review.js 的 lockGradedAnswer：揭示后禁用输入框与提交按钮，只留
// 「记 0 分并继续」——它提交 revealed=1，也就是这个测试模拟的请求。
func TestReviewRevealedGradedCardCountsAsAgain(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Graded deck")
	// typed 题型：正确答案是 Paris，走判分器会拿 Good；揭示后必须记 Again。
	seedGradedNote(t, db, deck.ID, "typed", map[string]any{"prompt": "法国首都？", "answer": "Paris"})

	page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /review status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	cardID := attrValue(body, "card_id")
	if cardID == "" {
		t.Fatalf("review page rendered no card: %s", snippet(body))
	}
	// 页面上必须有那个"记 0 分"出口（默认隐藏，由 JS 在揭示时显示）。
	if !strings.Contains(body, `id="review-zero"`) {
		t.Errorf("graded card is missing the zero-score button")
	}

	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {cardID},
		"revealed":         {"1"},
		"expected_version": {"0"},
		"done":             {"0"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /review/answer (revealed) status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 库里那一条必须是 Again（1），且 grade_source 记 self（没有发生机器判分）。
	var row store.Review
	if err := db.Where("user_id = ?", ownerID).Order("id desc").First(&row).Error; err != nil {
		t.Fatalf("load review row: %v", err)
	}
	if row.Rating != 1 {
		t.Errorf("rating = %d, want 1 (Again) — a revealed answer must not be graded", row.Rating)
	}
	if row.GradeSource != "self" {
		t.Errorf("grade_source = %q, want \"self\" (nothing was graded)", row.GradeSource)
	}
	want, err := strconv.ParseUint(cardID, 10, 64)
	if err != nil {
		t.Fatalf("parse card id %q: %v", cardID, err)
	}
	if row.CardID != want {
		t.Errorf("review row card_id = %d, want %d", row.CardID, want)
	}
	// 状态必须被推进：Again 之后卡进入（再）学习阶段，而不是停在 new。
	var st store.CardState
	if err := db.Where("user_id = ? AND card_id = ?", ownerID, row.CardID).First(&st).Error; err != nil {
		t.Fatalf("card state after revealed submit: %v", err)
	}
	if st.State == "new" {
		t.Errorf("card state = %q, want it scheduled (not new) after a revealed submit", st.State)
	}
}
