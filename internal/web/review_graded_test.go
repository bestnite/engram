package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// seedGradedNote 建一张作答类 note（M3-12 验收用）。
func seedGradedNote(t *testing.T, db *gorm.DB, deckID uint64, kind string, fields map[string]any) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: kind}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note, fields); err != nil {
		t.Fatalf("create %s note: %v", kind, err)
	}
	return note
}

// TestReviewGradedNumericAcceptance 是 M3-12 的核心验收：对 numeric 卡答对、答错、
// 容差边界三例走一遍复习页，断言 reviews 行的 grade_source 与档位符合预期，
// 且判分细节写入 grade_detail_json。容差边界（恰好等于容差）算对，超出则算错。
func TestReviewGradedNumericAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Numeric deck")

	cases := []struct {
		name       string
		answer     string
		wantRating schedule.Rating
		wantScore  float64
	}{
		{"correct", "50", schedule.Good, 1},
		{"wrong", "47", schedule.Again, 0},
		{"tolerance_below_exact", "50.5", schedule.Good, 1},      // 差值恰好 = 绝对容差，算对
		{"tolerance_above_boundary", "50.51", schedule.Again, 0}, // 差值略超容差，算错
	}
	for _, tc := range cases {
		seedGradedNote(t, db, deck.ID, "numeric", map[string]any{
			"prompt": "分数 1/2 化成百分数是多少？", "value": 50.0, "tolerance_absolute": 0.5, "unit": "%",
		})
		page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
		body := page.Body.String()
		cardID := attrValue(body, "card_id")
		if cardID == "" {
			t.Fatalf("%s: no card rendered: %s", tc.name, snippet(body))
		}
		if !strings.Contains(body, `id="review-graded"`) {
			t.Fatalf("%s: numeric card did not render graded controls: %s", tc.name, snippet(body))
		}
		if strings.Contains(body, "data-rating=") {
			t.Errorf("%s: graded card still renders the four self-ratings", tc.name)
		}

		rec := postForm(t, srv, "/review/answer", url.Values{
			"csrf_token":       {csrf},
			"card_id":          {cardID},
			"deck":             {u64str(deck.ID)},
			"expected_version": {attrValue(body, "expected_version")},
			"done":             {attrValue(body, "done")},
			"elapsed_ms":       {"1200"},
			"answer":           {tc.answer},
		}, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: POST /review/answer status = %d, want 200 (body %s)", tc.name, rec.Code, snippet(rec.Body.String()))
		}
		resultBody := rec.Body.String()
		if !strings.Contains(resultBody, `id="review-result"`) || !strings.Contains(resultBody, `id="review-continue"`) {
			t.Fatalf("%s: graded response has no result panel / continue button: %s", tc.name, snippet(resultBody))
		}
		if tc.wantScore >= 1.0 {
			if !strings.Contains(resultBody, "border-emerald-") {
				t.Errorf("%s: correct answer missing emerald border styling: %s", tc.name, snippet(resultBody))
			}
		} else if tc.wantScore == 0.0 {
			if !strings.Contains(resultBody, "border-rose-") {
				t.Errorf("%s: wrong answer missing rose border styling: %s", tc.name, snippet(resultBody))
			}
		}

		var rev store.Review
		if err := db.Where("card_id = ?", cardID).First(&rev).Error; err != nil {
			t.Fatalf("%s: load review row: %v", tc.name, err)
		}
		if rev.GradeSource != schedule.GradeSourceTyped {
			t.Errorf("%s: grade_source = %q, want %q", tc.name, rev.GradeSource, schedule.GradeSourceTyped)
		}
		if rev.Rating != int(tc.wantRating) {
			t.Errorf("%s: rating = %d, want %d", tc.name, rev.Rating, tc.wantRating)
		}
		if rev.GradeDetailJSON == nil {
			t.Fatalf("%s: grade_detail_json is NULL", tc.name)
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(*rev.GradeDetailJSON), &detail); err != nil {
			t.Fatalf("%s: grade_detail_json is not valid JSON: %v", tc.name, err)
		}
		if got, ok := detail["score"].(float64); !ok || got != tc.wantScore {
			t.Errorf("%s: detail score = %v, want %v", tc.name, detail["score"], tc.wantScore)
		}
		if got, ok := detail["given"].(string); !ok || got != tc.answer {
			t.Errorf("%s: detail given = %v, want %q", tc.name, detail["given"], tc.answer)
		}
		t.Logf("%s: card=%s answer=%q -> rating=%d grade_source=%q grade_detail_json=%s response_has_result=%t",
			tc.name, cardID, tc.answer, rev.Rating, rev.GradeSource, *rev.GradeDetailJSON, strings.Contains(resultBody, `id="review-result"`))

		// “继续”只换下一张卡，不再写第二条 review。
		next := postForm(t, srv, "/review/answer", url.Values{
			"csrf_token": {csrf},
			"card_id":    {cardID},
			"deck":       {u64str(deck.ID)},
			"done":       {attrValue(resultBody, "done")},
			"action":     {"next"},
		}, cookies)
		if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), `id="review-area"`) {
			t.Fatalf("%s: continue status = %d, want 200 with a review area", tc.name, next.Code)
		}
		if strings.Contains(next.Body.String(), `id="review-result"`) {
			t.Errorf("%s: continue still shows the result panel", tc.name)
		}
		var reviewsAfter int64
		db.Model(&store.Review{}).Where("card_id = ?", cardID).Count(&reviewsAfter)
		if reviewsAfter != 1 {
			t.Errorf("%s: continue wrote an extra review row (%d)", tc.name, reviewsAfter)
		}
	}
}

// TestReviewGradedOtherTypesAcceptance 覆盖 typed / choice_single / choice_multi / true_false：
// 正确与错误各一例，断言 grade_source 与档位（多选部分对映射为 Hard）。
func TestReviewGradedOtherTypesAcceptance(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Graded deck")

	type answerCase struct {
		name       string
		kind       string
		fields     map[string]any
		answers    []string
		wantRating schedule.Rating
	}
	cases := []answerCase{
		{"typed_correct", "typed", map[string]any{"prompt": "法国首都？", "answer": "Paris", "accept": []string{"巴黎"}}, []string{"paris"}, schedule.Good},
		{"typed_wrong", "typed", map[string]any{"prompt": "法国首都？", "answer": "Paris"}, []string{"London"}, schedule.Again},
		{"single_correct", "choice_single", map[string]any{"question": "2+2=?", "options": []string{"3", "4", "5"}, "answer": 1}, []string{"1"}, schedule.Good},
		{"single_wrong", "choice_single", map[string]any{"question": "2+2=?", "options": []string{"3", "4", "5"}, "answer": 1}, []string{"2"}, schedule.Again},
		{"multi_partial", "choice_multi", map[string]any{"question": "偶数？", "options": []string{"1", "2", "3", "4"}, "answers": []any{1, 3}}, []string{"1"}, schedule.Hard},
		{"multi_full", "choice_multi", map[string]any{"question": "偶数？", "options": []string{"1", "2", "3", "4"}, "answers": []any{1, 3}}, []string{"3", "1"}, schedule.Good},
		{"true_false_correct", "true_false", map[string]any{"statement": "地球是圆的", "answer": true}, []string{"true"}, schedule.Good},
		{"true_false_wrong", "true_false", map[string]any{"statement": "地球是圆的", "answer": true}, []string{"false"}, schedule.Again},
	}
	for _, tc := range cases {
		seedGradedNote(t, db, deck.ID, tc.kind, tc.fields)
		page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
		body := page.Body.String()
		cardID := attrValue(body, "card_id")
		if cardID == "" {
			t.Fatalf("%s: no card rendered: %s", tc.name, snippet(body))
		}
		if !strings.Contains(body, `id="review-graded"`) {
			t.Fatalf("%s: %s card did not render graded controls: %s", tc.name, tc.kind, snippet(body))
		}
		rec := postForm(t, srv, "/review/answer", url.Values{
			"csrf_token":       {csrf},
			"card_id":          {cardID},
			"deck":             {u64str(deck.ID)},
			"expected_version": {attrValue(body, "expected_version")},
			"done":             {attrValue(body, "done")},
			"answer":           tc.answers,
		}, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: POST status = %d, want 200 (body %s)", tc.name, rec.Code, snippet(rec.Body.String()))
		}
		resultBody := rec.Body.String()
		if tc.wantRating == schedule.Good {
			if !strings.Contains(resultBody, "border-emerald-") {
				t.Errorf("%s: good rating missing emerald border styling: %s", tc.name, snippet(resultBody))
			}
		} else if tc.wantRating == schedule.Again {
			if !strings.Contains(resultBody, "border-rose-") {
				t.Errorf("%s: again rating missing rose border styling: %s", tc.name, snippet(resultBody))
			}
		} else if tc.wantRating == schedule.Hard {
			if !strings.Contains(resultBody, "border-amber-") {
				t.Errorf("%s: hard partial rating missing amber border styling: %s", tc.name, snippet(resultBody))
			}
		}
		var rev store.Review
		if err := db.Where("card_id = ?", cardID).First(&rev).Error; err != nil {
			t.Fatalf("%s: load review row: %v", tc.name, err)
		}
		if rev.GradeSource != schedule.GradeSourceTyped || rev.Rating != int(tc.wantRating) {
			t.Errorf("%s: grade_source=%q rating=%d, want typed/%d", tc.name, rev.GradeSource, rev.Rating, tc.wantRating)
		}
		t.Logf("%s: kind=%s card=%s -> rating=%d grade_source=%q detail=%v", tc.name, tc.kind, cardID, rev.Rating, rev.GradeSource, derefDetail(rev.GradeDetailJSON))
	}
}

// derefDetail 让测试日志可读：nil 详情打印 <nil>。
func derefDetail(raw *string) string {
	if raw == nil {
		return "<nil>"
	}
	return *raw
}
