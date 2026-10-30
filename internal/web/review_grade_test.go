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

// postJSONWithCSRF submits JSON with the session cookie and the X-CSRF-Token header (the uniform shape of SPA writes).
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

// gradeFeedbackBody is the external shape of the grading feedback (aligned with the feedback object the handler returns).
type gradeFeedbackBody struct {
	Verdict    string  `json:"verdict"`
	Score      float64 `json:"score"`
	Rating     int     `json:"rating"`
	AnswerHTML string  `json:"answer_html"`
	Given      string  `json:"given"`
	Parsed     string  `json:"parsed"`
}

// gradeResponseBody is the external shape of the grading entry point (the grade and give-up paths share the state fields).
type gradeResponseBody struct {
	CardID    string             `json:"card_id"`
	State     string             `json:"state"`
	Version   int                `json:"version"`
	Remaining int                `json:"remaining"`
	Cards     []api.DueCard      `json:"cards"`
	Feedback  *gradeFeedbackBody `json:"feedback"`
	GaveUp    bool               `json:"gave_up"`
}

// decodeGrade decodes the grading response; a failure fails the test.
func decodeGrade(t *testing.T, rec *httptest.ResponseRecorder) gradeResponseBody {
	t.Helper()
	var body gradeResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grade response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// reviewRowForCard reads the single review row of a card; missing means the test fails.
func reviewRowForCard(t *testing.T, srv *Server, cardID uint64) store.Review {
	t.Helper()
	var rev store.Review
	if err := srv.db.Where("card_id = ?", cardID).First(&rev).Error; err != nil {
		t.Fatalf("load review row for card %d: %v", cardID, err)
	}
	return rev
}

// TestGradeNumericAcceptance covers the core of the SPA grading entry point: for numeric, right / wrong / tolerance boundary
// are all graded by the server-side grader, and the row written has grade_source=typed with the grading detail.
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

// TestGradeOtherTypesAcceptance covers typed / choice_single / choice_multi / true_false:
// one correct and one wrong case each (a partly right multi-select → partial/Hard), with the grade decided by the grader and the mapping.
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

// TestGradeAuthorizationAndCSRF covers the rejection paths: no session, missing/wrong CSRF, bearer, unreadable deck,
// card outside the scope, a self-assessed type on the grading entry point, an answer that cannot be graded. No rejection may write progress.
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

	// A bearer carrying review scope must not pass either: the SPA grading entry point only accepts the session cookie.
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

	// A self-assessed type must not go through the grading entry point.
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": basicCardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "Back",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-assess card on grade endpoint = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// Card outside the selected scope.
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{otherDeck.PublicID}, "answer": "a",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-scope card = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// A zero deck id is not a legal scope (illegal public id shape).
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{"0"}, "answer": "a",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("zero deck id = %d, want 400", rec.Code)
	}

	// choice_single without an answer cannot be graded: 400 and nothing is written.
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

	// An unauthorized user cannot read the deck even with the card id in hand.
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "grade-outsider")
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardPub, "expected_version": 0, "deck": []string{deck.PublicID}, "answer": "a",
	}, outsiderCookies, outsiderCSRF)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("outsider status = %d, want 4xx (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestGradeVersionConflict covers the optimistic lock: a mismatched expected_version returns 409 and writes no second review.
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

// TestGradeRevealAndGiveUp covers the read-only reveal preview and "give up answering, log Again":
// reveal returns the sanitized correct answer and writes nothing; give_up writes an Again with grade_source=self.
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

// TestGradeListFeedbackBreakdown asserts a list question gets partial credit through the SPA grading entry point by the number of items recalled, with the feedback marking each item
// right or wrong (in item order, sanitized), and the render response gives one input box per item.
func TestGradeListFeedbackBreakdown(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA list")
	note := seedGradedNote(t, db, deck.ID, "list", map[string]any{"prompt": "primary colours", "items": []string{"red", "green", "<strong>blue</strong>|violet"}})
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
	}{{"red", true}, {"green", false}, {"blue / violet", true}}
	if len(fb.Breakdown) != len(want) {
		t.Fatalf("breakdown = %+v, want %d items", fb.Breakdown, len(want))
	}
	for i, w := range want {
		if !strings.Contains(fb.Breakdown[i].AnswerHTML, w.html) || fb.Breakdown[i].Correct != w.correct {
			t.Errorf("breakdown[%d] = %+v, want html containing %q correct=%v", i, fb.Breakdown[i], w.html, w.correct)
		}
	}
}

// TestSelfReviewStoresShortAnswer asserts the SPA self-assessment entry point passes the answer written for a short-answer question through to the service for archiving
// (the source is still self), while a self-assessed type that takes no answer is rejected if one is sent, with nothing written.
func TestSelfReviewStoresShortAnswer(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA short answer")
	short := seedGradedNote(t, db, deck.ID, "short_answer", map[string]any{"prompt": "why?", "reference": "because"})
	basic := seedBasic(t, db, deck.ID, "Q", "A")

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, short.ID), "rating": 3, "expected_version": 0,
		"deck": []string{deck.PublicID}, "answer": "because of X",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST answer = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rev := reviewRowForCard(t, srv, cardIDOfNote(t, db, short.ID))
	if rev.GradeSource != schedule.GradeSourceSelf || rev.Rating != 3 || derefDetail(rev.GradeDetailJSON) != `{"answer":"because of X","self_assessed":true}` {
		t.Errorf("review = source %q rating %d detail %s, want self/3 with the written answer", rev.GradeSource, rev.Rating, derefDetail(rev.GradeDetailJSON))
	}

	rec = postJSONWithCSRF(t, srv, "/api/v1/review/answer", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, basic.ID), "rating": 3, "expected_version": 0,
		"deck": []string{deck.PublicID}, "answer": "A",
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST answer on basic = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var n int64
	if err := db.Model(&store.Review{}).Where("card_id = ?", cardIDOfNote(t, db, basic.ID)).Count(&n).Error; err != nil || n != 0 {
		t.Errorf("rejected basic submission wrote %d review rows (err %v)", n, err)
	}
}
