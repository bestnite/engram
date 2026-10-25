package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// renderReviewCard posts one card to the SPA render endpoint and decodes the response.
func renderReviewCard(t *testing.T, srv *Server, deckID, cardID string, cookies []*http.Cookie, csrf string) reviewRenderBody {
	t.Helper()
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
		"card_id": cardID, "deck": []string{deckID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body reviewRenderBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// validOptionOrder reports whether order is a permutation of 0..n-1.
func validOptionOrder(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, idx := range order {
		if idx < 0 || idx >= n || seen[idx] {
			return false
		}
		seen[idx] = true
	}
	return true
}

// TestReviewRenderPublishesOptionOrder pins the render contract for choice cards: option_order is a permutation of the
// stored option indices, repeated renders of an unreviewed card agree, options_html keeps stored order, and a card type
// without option controls publishes null.
func TestReviewRenderPublishesOptionOrder(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Option order deck")
	note := seedGradedNote(t, db, deck.ID, "choice_single", map[string]any{
		"question": "pick-question",
		"options":  []string{"opt-alpha", "opt-beta", "opt-gamma"},
		"answer":   1,
	})
	cardID := cardPublicIDOfNote(t, db, note.ID)

	first := renderReviewCard(t, srv, deck.PublicID, cardID, cookies, csrf)
	if !validOptionOrder(first.OptionOrder, 3) {
		t.Fatalf("option_order = %v, want a permutation of 0..2", first.OptionOrder)
	}
	if len(first.OptionsHTML) != 3 || first.OptionsHTML[1] != "<p>opt-beta</p>\n" {
		t.Errorf("options_html = %#v, want stored option order", first.OptionsHTML)
	}
	second := renderReviewCard(t, srv, deck.PublicID, cardID, cookies, csrf)
	if !equalOptionOrder(first.OptionOrder, second.OptionOrder) {
		t.Errorf("an unreviewed card changed order between renders: %v then %v", first.OptionOrder, second.OptionOrder)
	}
	if strings.Join(second.OptionsHTML, "|") != strings.Join(first.OptionsHTML, "|") {
		t.Errorf("options_html changed between renders: %#v then %#v", first.OptionsHTML, second.OptionsHTML)
	}

	basic := seedBasic(t, db, deck.ID, "basic-front", "basic-back")
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, basic.ID), "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("basic render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode basic render response: %v", err)
	}
	if got := string(raw["option_order"]); got != "null" {
		t.Errorf("non-choice option_order = %s, want null", got)
	}
}

// TestReviewRenderOptionOrderChangesAfterGrade covers the lifecycle: one graded submission advances the state version,
// so the next render of the same card returns a different display order; the submitted answer is still a stored index
// and a correct pick scores 1.
func TestReviewRenderOptionOrderChangesAfterGrade(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Option order grade deck")
	note := seedGradedNote(t, db, deck.ID, "choice_single", map[string]any{
		"question": "pick-question",
		"options":  []string{"opt-alpha", "opt-beta", "opt-gamma", "opt-delta"},
		"answer":   2,
	})
	cardID := cardPublicIDOfNote(t, db, note.ID)

	before := renderReviewCard(t, srv, deck.PublicID, cardID, cookies, csrf)
	if !validOptionOrder(before.OptionOrder, 4) {
		t.Fatalf("option_order = %v, want a permutation of 0..3", before.OptionOrder)
	}

	// Stored index 2 is the correct option; the display order plays no part in grading.
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardID, "deck": []string{deck.PublicID}, "expected_version": 0, "answer": 2,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("grade = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var graded struct {
		Feedback struct {
			Score *float64 `json:"score"`
		} `json:"feedback"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &graded); err != nil {
		t.Fatalf("decode grade response: %v", err)
	}
	if graded.Feedback.Score == nil || *graded.Feedback.Score != 1 {
		t.Errorf("grade score = %v, want 1 for the correct stored index", graded.Feedback.Score)
	}

	after := renderReviewCard(t, srv, deck.PublicID, cardID, cookies, csrf)
	if !validOptionOrder(after.OptionOrder, 4) {
		t.Fatalf("post-review option_order = %v, want a permutation of 0..3", after.OptionOrder)
	}
	if equalOptionOrder(before.OptionOrder, after.OptionOrder) {
		t.Errorf("order did not change after a review: %v", after.OptionOrder)
	}
}

// TestChoiceMultiRevealFollowsOptionOrder asserts the reveal lists the correct options in display order: the back and
// the reveal answer use the same order the answer controls showed, while options_html itself keeps stored order.
func TestChoiceMultiRevealFollowsOptionOrder(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Multi order deck")
	options := []string{"opt-alpha", "opt-beta", "opt-gamma", "opt-delta"}
	note := seedGradedNote(t, db, deck.ID, "choice_multi", map[string]any{
		"question": "pick-question",
		"options":  options,
		"answers":  []any{0, 2},
	})
	cardID := cardPublicIDOfNote(t, db, note.ID)

	body := renderReviewCard(t, srv, deck.PublicID, cardID, cookies, csrf)
	if !validOptionOrder(body.OptionOrder, len(options)) {
		t.Fatalf("option_order = %v, want a permutation of 0..%d", body.OptionOrder, len(options)-1)
	}
	if len(body.OptionsHTML) != len(options) || !strings.Contains(body.OptionsHTML[2], "opt-gamma") {
		t.Errorf("options_html = %#v, want stored option order", body.OptionsHTML)
	}

	// The correct options in display order: walk the display slots and keep the ones the stored answer marks correct.
	var want []string
	for _, stored := range body.OptionOrder {
		if stored == 0 || stored == 2 {
			want = append(want, options[stored])
		}
	}
	if len(want) != 2 {
		t.Fatalf("display order %v does not place both correct options; got %v", body.OptionOrder, want)
	}
	assertInOrder(t, body.BackHTML, want)

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/grade", map[string]any{
		"card_id": cardID, "deck": []string{deck.PublicID}, "action": "reveal",
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var revealed struct {
		AnswerHTML string `json:"answer_html"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &revealed); err != nil {
		t.Fatalf("decode reveal response: %v", err)
	}
	assertInOrder(t, revealed.AnswerHTML, want)
}

// assertInOrder asserts each fragment appears in html and the fragments appear in the given order.
func assertInOrder(t *testing.T, html string, fragments []string) {
	t.Helper()
	pos := -1
	for _, fragment := range fragments {
		next := strings.Index(html, fragment)
		if next < 0 {
			t.Fatalf("HTML %q is missing %q", html, fragment)
		}
		if next < pos {
			t.Fatalf("HTML %q lists %q out of display order %v", html, fragment, fragments)
		}
		pos = next
	}
}

// equalOptionOrder reports whether two display orders are identical.
func equalOptionOrder(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
