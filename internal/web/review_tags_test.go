package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// TestReviewPageTagScope 覆盖复习页地址上的标签范围：单卡组带标签放行；没有卡组或跨多个卡组时
// 带标签返回 400，不静默忽略标签。
func TestReviewPageTagScope(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Idioms A")
	deckB := seedReviewDeck(t, db, ownerID, "Idioms B")
	seedBasic(t, db, deckA.ID, "Q", "A", "创新")

	cases := []struct {
		name  string
		query url.Values
		want  int
	}{
		{"one deck with tags", url.Values{"deck": {deckA.PublicID}, "tag": {"创新", "文化传承"}}, http.StatusOK},
		{"blank tag is ignored", url.Values{"tag": {" "}}, http.StatusOK},
		{"tag without deck", url.Values{"tag": {"创新"}}, http.StatusBadRequest},
		{"tag across two decks", url.Values{"deck": {deckA.PublicID, deckB.PublicID}, "tag": {"创新"}}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		rec := getWithCookies(t, srv, "/review?"+tc.query.Encode(), cookies)
		if rec.Code != tc.want {
			t.Errorf("%s: GET /review status = %d, want %d (body %s)", tc.name, rec.Code, tc.want, snippet(rec.Body.String()))
		}
	}
}

// TestReviewAnswerKeepsTagScope 断言评分后重建的队列沿用请求里的标签：只剩带该标签的卡，
// 不会退化成整个卡组。
func TestReviewAnswerKeepsTagScope(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Idioms")
	first := seedBasic(t, db, deck.ID, "Q1", "A1", "文化传承")
	second := seedBasic(t, db, deck.ID, "Q2", "A2", "文化传承", "高频")
	seedBasic(t, db, deck.ID, "Q3", "A3", "创新")
	seedBasic(t, db, deck.ID, "Q4", "A4")

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, first.ID), "rating": 4, "expected_version": 0,
		"deck": []string{deck.PublicID}, "tags": []string{"文化传承"},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer with tags = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body struct {
		Cards []struct {
			CardID string `json:"card_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode answer response: %v", err)
	}
	want := cardPublicIDOfNote(t, db, second.ID)
	if len(body.Cards) != 1 || body.Cards[0].CardID != want {
		t.Fatalf("queue after answer = %+v, want only card %s (the other 文化传承 card)", body.Cards, want)
	}
}

// TestReviewActionsRejectTagsAcrossDecks 覆盖 JSON 动作入口的负例：带标签却跨多个卡组时 400，
// 且不执行动作（埋藏不写 card_states）。
func TestReviewActionsRejectTagsAcrossDecks(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Idioms A")
	deckB := seedReviewDeck(t, db, ownerID, "Idioms B")
	note := seedBasic(t, db, deckA.ID, "Q", "A", "创新")
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	for _, path := range []string{"/api/v1/review/bury", "/api/v1/review/render"} {
		rec := postJSONWithCSRF(t, srv, path, map[string]any{
			"card_id": cardPub, "deck": []string{deckA.PublicID, deckB.PublicID}, "tags": []string{"创新"},
		}, cookies, csrf)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s with tags across two decks = %d, want 400 (body %s)", path, rec.Code, snippet(rec.Body.String()))
		}
	}
	var states int64
	db.Table("card_states").Where("card_id = ?", cardIDOfNote(t, db, note.ID)).Count(&states)
	if states != 0 {
		t.Errorf("card_states rows after rejected bury = %d, want 0", states)
	}
}
