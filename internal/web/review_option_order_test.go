package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
)

// TestChoiceMultiRevealFollowsOptionOrder asserts the reveal lists the correct options in the same display order the
// answer controls use: both the back face and the reveal answer are rendered with the card's option order, while the
// stored answer stays a set of stored option indices.
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
	order := api.OptionOrder(len(options), ownerID, cardIDOfNote(t, db, note.ID), 0)

	// The correct options in display order: walk the display slots and keep the ones the stored answer marks correct.
	var want []string
	for _, stored := range order {
		if stored == 0 || stored == 2 {
			want = append(want, options[stored])
		}
	}
	if len(want) != 2 {
		t.Fatalf("display order %v does not place both correct options; got %v", order, want)
	}

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
