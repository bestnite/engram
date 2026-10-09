package mcp

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestSubmitReviewGradingRulesOverMCP 断言 MCP 与 REST 走同一组评分规则：作答类题型的自评分被拒、
// 伪造的 grade_source 被拒，提交作答则由服务端判分并返回判定。
func TestSubmitReviewGradingRulesOverMCP(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "grader")
	deck := seedDeck(t, db, u.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview})
	cs := connect(t, ts.URL, key)
	body := `{"notes":[{"kind":"true_false","fields":{"statement":"water is wet","answer":true},"external_ref":"tf:1"},` +
		`{"kind":"basic","fields":{"front":"q","back":"a"},"external_ref":"b:1"}]}`
	if st, raw := rest(t, ts.URL, http.MethodPost, fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID), key, body); st != 200 {
		t.Fatalf("seed import status = %d body %v", st, raw)
	}
	_, exp := rest(t, ts.URL, http.MethodGet, fmt.Sprintf("/api/v1/export?deck=%s&format=json", deck.PublicID), key, "")
	cardByKind := map[string]string{}
	for _, raw := range exp["cards"].([]any) {
		c := raw.(map[string]any)
		cardByKind[c["kind"].(string)] = c["card_id"].(string)
	}

	rejected := []struct {
		name string
		args map[string]any
		want string
	}{
		{"graded card with a rating", map[string]any{"card_id": cardByKind["true_false"], "rating": 4}, "graded by the server"},
		{"forged grade_source", map[string]any{"card_id": cardByKind["basic"], "rating": 3, "grade_source": "llm"}, "grade_source"},
	}
	for _, tc := range rejected {
		_, isErr, text := callTool(t, cs, "submit_review", tc.args)
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s: isErr=%v text=%q, want an error mentioning %q", tc.name, isErr, text, tc.want)
		}
	}
	var rows int64
	db.Model(&store.Review{}).Count(&rows)
	if rows != 0 {
		t.Fatalf("rejected submissions wrote %d review rows", rows)
	}

	out, isErr, text := callTool(t, cs, "submit_review", map[string]any{"card_id": cardByKind["true_false"], "answer": true})
	if isErr {
		t.Fatalf("graded submission error: %s", text)
	}
	grade, _ := out["grade"].(map[string]any)
	if grade["verdict"] != "correct" || grade["rating"] != float64(3) {
		t.Errorf("grade = %v, want verdict correct rating 3", grade)
	}
	var review store.Review
	if err := db.First(&review).Error; err != nil {
		t.Fatalf("load review: %v", err)
	}
	if review.GradeSource != "typed" {
		t.Errorf("grade_source = %q, want typed", review.GradeSource)
	}
}
