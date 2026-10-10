package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedKindCard 建一张指定题型的卡（无 card_states，新卡），返回 card。
func seedKindCard(t *testing.T, db *gorm.DB, deckID uint64, kind, fieldsJSON, template string) store.Card {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{DeckID: deckID, Kind: kind, FieldsJSON: fieldsJSON, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: template, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c
}

// TestSubmitReviewGradingRules 覆盖评分来源的规则（REST 入口，service 层同一实现也服务 MCP 与网页）：
//   - 作答类题型不接受自评分，只能提交作答由服务端判分，或放弃作答；
//   - grade_source 只能是 self 或不填，客户端不能把自评伪装成机器判分；
//   - 自评题型不接受作答。
//
// 被拒的请求一条复习日志都不能写。
func TestSubmitReviewGradingRules(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		fields     string
		template   string
		body       string
		wantStatus int
		wantCode   string
		wantSource string
		wantRating int
		wantGrade  string
	}{
		{name: "graded card rejects a self rating", kind: "true_false", fields: `{"statement":"s","answer":true}`, template: "forward",
			body: `"rating":4`, wantStatus: http.StatusBadRequest, wantCode: CodeGradingRequired},
		{name: "graded card is graded from the answer", kind: "true_false", fields: `{"statement":"s","answer":true}`, template: "forward",
			body: `"answer":true`, wantStatus: http.StatusOK, wantSource: "typed", wantRating: 3, wantGrade: "correct"},
		{name: "wrong answer grades as Again", kind: "true_false", fields: `{"statement":"s","answer":true}`, template: "forward",
			body: `"answer":false`, wantStatus: http.StatusOK, wantSource: "typed", wantRating: 1, wantGrade: "incorrect"},
		{name: "giving up records Again as self", kind: "true_false", fields: `{"statement":"s","answer":true}`, template: "forward",
			body: `"give_up":true`, wantStatus: http.StatusOK, wantSource: "self", wantRating: 1},
		{name: "ungradable answer is rejected", kind: "true_false", fields: `{"statement":"s","answer":true}`, template: "forward",
			body: `"answer":"yes"`, wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "cloze card rejects a self rating", kind: "cloze", fields: `{"text":"{{c1::Paris}} and {{c2::Rome}}"}`, template: "cloze:2",
			body: `"rating":3`, wantStatus: http.StatusBadRequest, wantCode: CodeGradingRequired},
		{name: "cloze card is graded against its own cloze number", kind: "cloze", fields: `{"text":"{{c1::Paris}} and {{c2::Rome}}"}`, template: "cloze:2",
			body: `"answer":["rome"]`, wantStatus: http.StatusOK, wantSource: "typed", wantRating: 3, wantGrade: "correct"},
		{name: "cloze answer for another cloze number is wrong", kind: "cloze", fields: `{"text":"{{c1::Paris}} and {{c2::Rome}}"}`, template: "cloze:2",
			body: `"answer":["Paris"]`, wantStatus: http.StatusOK, wantSource: "typed", wantRating: 1, wantGrade: "incorrect"},
		{name: "forged grade_source is rejected", kind: "basic", fields: `{"front":"q","back":"a"}`, template: "forward",
			body: `"rating":3,"grade_source":"llm"`, wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "self-assessed card rejects an answer", kind: "basic", fields: `{"front":"q","back":"a"}`, template: "forward",
			body: `"answer":"a"`, wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "self-assessed card takes a rating", kind: "basic", fields: `{"front":"q","back":"a"}`, template: "forward",
			body: `"rating":3,"grade_source":"self"`, wantStatus: http.StatusOK, wantSource: "self", wantRating: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "grader", store.RoleUser)
			deck := seedDeck(t, env.db, user.ID)
			card := seedKindCard(t, env.db, deck.ID, tc.kind, tc.fields, tc.template)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)

			body := fmt.Sprintf(`{"card_id":%q,"expected_version":0,%s}`, card.PublicID, tc.body)
			status, raw := doJSON(t, env.router(), http.MethodPost, "/api/v1/review", k.Plaintext, body)
			if status != tc.wantStatus {
				t.Fatalf("POST /api/v1/review status = %d, want %d (body %s)", status, tc.wantStatus, raw)
			}
			var reviews []store.Review
			if err := env.db.Where("card_id = ?", card.ID).Find(&reviews).Error; err != nil {
				t.Fatalf("load reviews: %v", err)
			}
			if tc.wantStatus != http.StatusOK {
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(raw, &env)
				if env.Error.Code != tc.wantCode {
					t.Errorf("error code = %q, want %q", env.Error.Code, tc.wantCode)
				}
				if len(reviews) != 0 {
					t.Errorf("a rejected submission wrote %d review rows", len(reviews))
				}
				return
			}
			if len(reviews) != 1 || reviews[0].GradeSource != tc.wantSource || reviews[0].Rating != tc.wantRating {
				t.Fatalf("reviews = %+v, want one row with source %s rating %d", reviews, tc.wantSource, tc.wantRating)
			}
			var res SubmitReviewResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			gotGrade := ""
			if res.Grade != nil {
				gotGrade = res.Grade.Verdict
			}
			if gotGrade != tc.wantGrade {
				t.Errorf("grade verdict = %q, want %q", gotGrade, tc.wantGrade)
			}
			if tc.wantSource == "typed" && (reviews[0].GradeDetailJSON == nil || *reviews[0].GradeDetailJSON == "") {
				t.Error("server-graded review has no grade detail")
			}
		})
	}
}
