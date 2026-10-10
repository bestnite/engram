package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// postJSONWithCSRF 带会话 cookie 与 X-CSRF-Token 头提交 JSON（SPA 写操作的统一形态）。
func postJSONWithCSRF(t *testing.T, srv *Server, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// gradeFeedbackBody 是判分反馈的对外形态（与 handler 返回的 feedback 对象对齐）。
type gradeFeedbackBody struct {
	Verdict    string  `json:"verdict"`
	Score      float64 `json:"score"`
	Rating     int     `json:"rating"`
	AnswerHTML string  `json:"answer_html"`
	Given      string  `json:"given"`
	Parsed     string  `json:"parsed"`
}

// gradeResponseBody 是判分入口的对外形态（判分与放弃两条路径共用状态字段）。
type gradeResponseBody struct {
	CardID    string             `json:"card_id"`
	State     string             `json:"state"`
	Version   int                `json:"version"`
	Remaining int                `json:"remaining"`
	Cards     []api.DueCard      `json:"cards"`
	Feedback  *gradeFeedbackBody `json:"feedback"`
	GaveUp    bool               `json:"gave_up"`
}

// decodeGrade 解码判分响应；失败即测试失败。
func decodeGrade(t *testing.T, rec *httptest.ResponseRecorder) gradeResponseBody {
	t.Helper()
	var body gradeResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grade response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// reviewRowForCard 读取某张卡唯一的 review 行；缺失即测试失败。
func reviewRowForCard(t *testing.T, srv *Server, cardID uint64) store.Review {
	t.Helper()
	var rev store.Review
	if err := srv.db.Where("card_id = ?", cardID).First(&rev).Error; err != nil {
		t.Fatalf("load review row for card %d: %v", cardID, err)
	}
	return rev
}

// TestGradeNumericAcceptance 覆盖 SPA 判分入口的核心：numeric 答对/答错/容差边界
// 都由服务端判分器决定档位，写库的 grade_source=typed 且带判分细节。
func TestGradeNumericAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA numeric")
	cases := []struct {
		name       string
		answer     string
		wantRating schedule.Rating
		wantScore  float64
		wantParsed string
	}{
		{"correct", "50", schedule.Good, 1, "50"},
		{"wrong", "47", schedule.Again, 0, "47"},
		{"tolerance_boundary", "50.5", schedule.Good, 1, "50.5"},
		{"tolerance_exceeded", "50.51", schedule.Again, 0, "50.51"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note := seedGradedNote(t, db, deck.ID, "numeric", map[string]any{
				"prompt": "分数 1/2 化成百分数是多少？", "value": 50.0, "tolerance_absolute": 0.5, "unit": "%",
			})
			cardID := cardIDOfNote(t, db, note.ID)
			cardPub := cardPublicIDOfNote(t, db, note.ID)

			rec := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
				"card_id": cardPub, "expected_version": 0, "elapsed_ms": 1200,
				"deck": []string{deck.PublicID}, "answer": tc.answer,
			}, cookies, csrf)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST grade status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			body := decodeGrade(t, rec)
			if body.Feedback == nil {
				t.Fatalf("numeric grade returned no feedback: %s", snippet(rec.Body.String()))
			}
			if body.Feedback.Score != tc.wantScore || body.Feedback.Rating != int(tc.wantRating) {
				t.Errorf("feedback = %+v, want score %v rating %d", *body.Feedback, tc.wantScore, tc.wantRating)
			}
			if body.Feedback.AnswerHTML == "" {
				t.Errorf("feedback answer_html is empty, want sanitized correct answer")
			}
			if body.Feedback.Parsed != tc.wantParsed {
				t.Errorf("feedback parsed = %q, want %q", body.Feedback.Parsed, tc.wantParsed)
			}
			if body.Feedback.Given != tc.answer {
				t.Errorf("feedback given = %q, want %q", body.Feedback.Given, tc.answer)
			}
			wantVerdict := "incorrect"
			if tc.wantScore >= 1 {
				wantVerdict = "correct"
			}
			if body.Feedback.Verdict != wantVerdict {
				t.Errorf("feedback verdict = %q, want %q", body.Feedback.Verdict, wantVerdict)
			}

			rev := reviewRowForCard(t, srv, cardID)
			if rev.GradeSource != schedule.GradeSourceTyped || rev.Rating != int(tc.wantRating) {
				t.Errorf("review grade_source=%q rating=%d, want typed/%d", rev.GradeSource, rev.Rating, tc.wantRating)
			}
			if rev.GradeDetailJSON == nil {
				t.Fatalf("review grade_detail_json is NULL")
			}
			var detail map[string]any
			if err := json.Unmarshal([]byte(*rev.GradeDetailJSON), &detail); err != nil {
				t.Fatalf("grade_detail_json is not valid JSON: %v", err)
			}
			if got, _ := detail["score"].(float64); got != tc.wantScore {
				t.Errorf("grade_detail score = %v, want %v", detail["score"], tc.wantScore)
			}
		})
	}
}

// TestGradeOtherTypesAcceptance 覆盖 typed / choice_single / choice_multi / true_false：
// 正确与错误（多选部分对 → partial/Hard）各一例，档位由判分器与映射决定。
func TestGradeOtherTypesAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA graded")
	cases := []struct {
		name        string
		kind        string
		fields      map[string]any
		answer      any
		wantRating  schedule.Rating
		wantVerdict string
		wantGiven   string
	}{
		{"typed_correct", "typed", map[string]any{"prompt": "法国首都？", "answer": "Paris", "accept": []string{"巴黎"}}, "paris", schedule.Good, "correct", "paris"},
		{"typed_wrong", "typed", map[string]any{"prompt": "法国首都？", "answer": "Paris"}, "London", schedule.Again, "incorrect", "London"},
		{"single_correct", "choice_single", map[string]any{"question": "2+2=?", "options": []string{"3", "4", "5"}, "answer": 1}, 1, schedule.Good, "correct", "4"},
		{"single_wrong", "choice_single", map[string]any{"question": "2+2=?", "options": []string{"3", "4", "5"}, "answer": 1}, 2, schedule.Again, "incorrect", "5"},
		{"multi_partial", "choice_multi", map[string]any{"question": "偶数？", "options": []string{"1", "2", "3", "4"}, "answers": []any{1, 3}}, []int{1}, schedule.Hard, "partial", "2"},
		{"multi_full", "choice_multi", map[string]any{"question": "偶数？", "options": []string{"1", "2", "3", "4"}, "answers": []any{1, 3}}, []int{3, 1}, schedule.Good, "correct", "4, 2"},
		{"true_false_correct", "true_false", map[string]any{"statement": "地球是圆的", "answer": true}, true, schedule.Good, "correct", "true"},
		{"true_false_wrong", "true_false", map[string]any{"statement": "地球是圆的", "answer": true}, false, schedule.Again, "incorrect", "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note := seedGradedNote(t, db, deck.ID, tc.kind, tc.fields)
			cardID := cardIDOfNote(t, db, note.ID)
			rec := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
				"card_id": cardPublicIDOfNote(t, db, note.ID), "expected_version": 0, "deck": []string{deck.PublicID}, "answer": tc.answer,
			}, cookies, csrf)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST grade status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			body := decodeGrade(t, rec)
			if body.Feedback == nil {
				t.Fatalf("%s: no feedback: %s", tc.name, snippet(rec.Body.String()))
			}
			if body.Feedback.Rating != int(tc.wantRating) || body.Feedback.Verdict != tc.wantVerdict {
				t.Errorf("%s: feedback rating=%d verdict=%q, want %d/%q", tc.name, body.Feedback.Rating, body.Feedback.Verdict, tc.wantRating, tc.wantVerdict)
			}
			if body.Feedback.Given != tc.wantGiven {
				t.Errorf("%s: feedback given = %q, want %q", tc.name, body.Feedback.Given, tc.wantGiven)
			}
			rev := reviewRowForCard(t, srv, cardID)
			if rev.GradeSource != schedule.GradeSourceTyped || rev.Rating != int(tc.wantRating) {
				t.Errorf("%s: review grade_source=%q rating=%d, want typed/%d", tc.name, rev.GradeSource, rev.Rating, tc.wantRating)
			}
		})
	}
}

// TestGradeAuthorizationAndCSRF 覆盖拒绝路径：无会话、缺/错 CSRF、bearer、不可读卡组、
// 卡不在范围内、自评类题型走判分入口、无法判分的作答。任一拒绝都不得写入进度。
func TestGradeAuthorizationAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA auth")
	otherDeck := seedReviewDeck(t, db, ownerID, "SPA other")
	note := seedGradedNote(t, db, deck.ID, "typed", map[string]any{"prompt": "p", "answer": "a"})
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	basicNote := seedBasic(t, db, deck.ID, "Front", "Back")
	basicCardPub := cardPublicIDOfNote(t, db, basicNote.ID)
	valid := map[string]any{"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "a"}

	denied := []struct {
		name    string
		body    any
		cookies []*http.Cookie
		csrf    string
	}{
		{"anonymous no session", valid, nil, ""},
		{"missing csrf", valid, cookies, ""},
		{"bad csrf", valid, cookies, "not-the-token"},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSONWithCSRF(t, srv, "/api/v1/review/grade", tc.body, tc.cookies, tc.csrf)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
		})
	}

	// bearer 携带 review scope 也不得通过：SPA 判分入口只认会话 cookie。
	key, err := store.NewAPIKeyStore(db).Create(t.Context(), store.CreateAPIKeyParams{
		UserID: ownerID, Name: "spa grade", Scopes: []string{store.ScopeReview},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/review/grade", bytes.NewReader(mustJSON(t, valid)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key.Plaintext)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bearer status = %d, want 403", rec.Code)
	}

	// 自评类题型不得走判分入口。
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": basicCardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "Back",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-assess card on grade endpoint = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 卡不在所选范围内。
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{otherDeck.PublicID}, "answer": "a",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-scope card = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 零卡组 id 不是合法范围（对外 id 形状非法）。
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{"0"}, "answer": "a",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("zero deck id = %d, want 400", rec.Code)
	}

	// choice_single 缺作答无法判分：400 且不写库。
	single := seedGradedNote(t, db, deck.ID, "choice_single", map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 0})
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, single.ID), "expected_version": 0, "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("choice_single without answer = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	var reviews, states int64
	db.Model(&store.Review{}).Count(&reviews)
	db.Model(&store.CardState{}).Count(&states)
	if reviews != 0 || states != 0 {
		t.Fatalf("denied requests wrote progress: reviews=%d states=%d, want 0/0", reviews, states)
	}

	// 无授权用户即使拿到卡片 id 也读不到卡组。
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "grade-outsider")
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "a",
	}, outsiderCookies, outsiderCSRF)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("outsider status = %d, want 4xx (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestGradeVersionConflict 覆盖乐观锁：expected_version 不匹配返回 409 且不写第二条 review。
func TestGradeVersionConflict(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA conflict")
	note := seedGradedNote(t, db, deck.ID, "typed", map[string]any{"prompt": "p", "answer": "a"})
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	first := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "a",
	}, cookies, csrf)
	if first.Code != http.StatusOK {
		t.Fatalf("first grade = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	stale := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "a",
	}, cookies, csrf)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale grade = %d, want 409 (body %s)", stale.Code, snippet(stale.Body.String()))
	}
	var reviews int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviews)
	if reviews != 1 {
		t.Fatalf("reviews after conflict = %d, want 1", reviews)
	}
}

// TestGradeRevealAndGiveUp 覆盖揭示的只读预览与「放弃作答记 Again」：
// reveal 返回清洗后的正确答案且不写库；give_up 写一条 grade_source=self 的 Again。
func TestGradeRevealAndGiveUp(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA reveal")
	note := seedGradedNote(t, db, deck.ID, "typed", map[string]any{"prompt": "prompt", "answer": "Paris"})
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	reveal := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "deck": []string{deck.PublicID}, "action": "reveal",
	}, cookies, csrf)
	if reveal.Code != http.StatusOK {
		t.Fatalf("reveal = %d, want 200 (body %s)", reveal.Code, snippet(reveal.Body.String()))
	}
	var revealed struct {
		Revealed   bool   `json:"revealed"`
		AnswerHTML string `json:"answer_html"`
	}
	if err := json.Unmarshal(reveal.Body.Bytes(), &revealed); err != nil {
		t.Fatal(err)
	}
	if !revealed.Revealed || revealed.AnswerHTML == "" {
		t.Fatalf("reveal body = %+v, want revealed with answer_html", revealed)
	}
	var reviewsAfterReveal int64
	db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsAfterReveal)
	if reviewsAfterReveal != 0 {
		t.Fatalf("reveal wrote %d reviews, want 0", reviewsAfterReveal)
	}

	giveUp := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "action": "give_up",
	}, cookies, csrf)
	if giveUp.Code != http.StatusOK {
		t.Fatalf("give_up = %d, want 200 (body %s)", giveUp.Code, snippet(giveUp.Body.String()))
	}
	body := decodeGrade(t, giveUp)
	if !body.GaveUp || body.Feedback != nil {
		t.Fatalf("give_up body = %+v, want gave_up without feedback", body)
	}
	rev := reviewRowForCard(t, srv, cardID)
	if rev.GradeSource != schedule.GradeSourceSelf || rev.Rating != int(schedule.Again) {
		t.Fatalf("give_up review grade_source=%q rating=%d, want self/%d", rev.GradeSource, rev.Rating, schedule.Again)
	}
}

// TestGradeListFeedbackBreakdown 断言列表题经 SPA 判分入口按想起的条数给部分分，反馈逐项标出
// 对错（按条目顺序、写法经清洗），且 render 响应按条数给出输入框个数。
func TestGradeListFeedbackBreakdown(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA list")
	note := seedGradedNote(t, db, deck.ID, "list", map[string]any{"prompt": "primary colours", "items": []string{"red", "green", "**blue**|violet"}})
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}, cookies, csrf)
	var rendered struct {
		Blanks []string `json:"blanks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rendered); err != nil || len(rendered.Blanks) != 3 {
		t.Fatalf("render blanks = %v (err %v), want three inputs (body %s)", rendered.Blanks, err, snippet(rec.Body.String()))
	}

	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": []string{"violet", "red", ""},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST grade status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body struct {
		Feedback struct {
			Verdict   string `json:"verdict"`
			Rating    int    `json:"rating"`
			Given     string `json:"given"`
			Breakdown []struct {
				AnswerHTML string `json:"answer_html"`
				Correct    bool   `json:"correct"`
			} `json:"breakdown"`
		} `json:"feedback"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grade response: %v", err)
	}
	fb := body.Feedback
	if fb.Verdict != "partial" || fb.Rating != int(schedule.Hard) || fb.Given != "violet, red" {
		t.Errorf("feedback = %+v, want partial/Hard with given %q", fb, "violet, red")
	}
	want := []struct {
		html    string
		correct bool
	}{{"red", true}, {"green", false}, {"<strong>blue</strong> / violet", true}}
	if len(fb.Breakdown) != len(want) {
		t.Fatalf("breakdown = %+v, want %d items", fb.Breakdown, len(want))
	}
	for i, w := range want {
		if !strings.Contains(fb.Breakdown[i].AnswerHTML, w.html) || fb.Breakdown[i].Correct != w.correct {
			t.Errorf("breakdown[%d] = %+v, want html containing %q correct=%v", i, fb.Breakdown[i], w.html, w.correct)
		}
	}
}
