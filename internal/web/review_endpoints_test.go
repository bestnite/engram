package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// The review page's same-origin JSON endpoints (card render, bury) and scope validation.
//
// The page itself only serves the app shell (see page_routes_test.go); data and writes all go through the endpoints pinned here;
// both write endpoints carry session CSRF, and the scope parameters must pass validation for the whole request.

type queueBody struct {
	CardID    string `json:"card_id"`
	Remaining int    `json:"remaining"`
	Cards     []struct {
		CardID string `json:"card_id"`
		DeckID string `json:"deck_id"`
	} `json:"cards"`
}

// reviewRenderBody is the external shape of the card render response.
type reviewRenderBody struct {
	CardID string `json:"card_id"`
	Front  struct {
		HTML string `json:"html"`
	} `json:"front"`
	FrontHTML string `json:"front_html"`
	BackHTML  string `json:"back_html"`
	// OptionsHTML is null for card types without options.
	OptionsHTML []string `json:"options_html"`
	// OptionOrder is the display order of a choice card's options, null for card types without option controls.
	OptionOrder []int  `json:"option_order"`
	EditHref    string `json:"edit_href"`
	Intervals   []struct {
		Rating  int   `json:"rating"`
		Seconds int64 `json:"seconds"`
	} `json:"intervals"`
}

// TestReviewRouteServesShell asserts a logged-in user hitting GET /review gets the SPA app shell, with the client router rendering the review page.
// Deck scope parameters are accepted as well: /review?deck=A&deck=B still returns the app shell.

// TestReviewRouteRejectsInvalidAndUnreadableScope asserts the scope decision runs before switching to the shell: a deck parameter that is
// non-numeric or 0 gives 400; an unreadable deck in the scope gives 403. Neither returns the app shell.
func TestReviewRouteRejectsInvalidAndUnreadableScope(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	foreign := seedReviewDeck(t, db, ownerID, "Foreign review deck")

	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"non_numeric", "/review?deck=abc", http.StatusBadRequest},
		{"zero", "/review?deck=0", http.StatusBadRequest},
		{"unreadable", "/review?deck=" + foreign.PublicID, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A stranger user hitting the owner's private deck: scope validation must fail for the whole request.
			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "review-scope-stranger-"+tc.name)
			rec := getWithCookies(t, srv, tc.path, strangerCookies)
			if rec.Code != tc.want {
				t.Fatalf("GET %s = %d, want %d (body %s)", tc.path, rec.Code, tc.want, snippet(rec.Body.String()))
			}
			if strings.Contains(rec.Body.String(), `<div id="app"></div>`) {
				t.Errorf("GET %s returned the SPA shell on a denied scope", tc.path)
			}
		})
	}

	// A legal scope still returns the app shell, proving the earlier failures came from scope validation and not from a wrong path.
	if rec := getWithCookies(t, srv, "/review?deck="+foreign.PublicID, cookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET review with own deck = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestReviewBuryRequiresCSRFAndReader asserts the rejection paths of the bury endpoint: no session 401, missing/wrong CSRF 403,
// a stranger who cannot read the deck 4xx, card outside the selected scope 400; no rejection may write card_states.
func TestReviewBuryRequiresCSRFAndReader(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Bury guard deck")
	other := seedReviewDeck(t, db, ownerID, "Bury other deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	valid := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}

	// No session.
	anon := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, nil, "")
	if anon.Code != http.StatusForbidden {
		t.Fatalf("bury without session = %d, want 403 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	// Missing CSRF and wrong CSRF.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("bury without CSRF = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, cookies, "not-the-token"); rec.Code != http.StatusForbidden {
		t.Errorf("bury with bad CSRF = %d, want 403", rec.Code)
	}
	// Card outside the selected scope.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{"card_id": cardPub, "deck": []string{other.PublicID}}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("bury out-of-scope card = %d, want 400", rec.Code)
	}
	// A stranger cannot read the deck even with the card id in hand.
	_, outsiderCookies, outsiderCSRF := createUserAndLogin(t, srv, db, "bury-outsider")
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", valid, outsiderCookies, outsiderCSRF); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("bury by outsider = %d, want 4xx", rec.Code)
	}

	var states int64
	db.Model(&store.CardState{}).Count(&states)
	if states != 0 {
		t.Fatalf("denied bury wrote %d card_states rows, want 0", states)
	}
}

// TestReviewBuryDefersCardAndKeepsScope asserts the core behavior of bury: reader and above can bury their own progress,
// bury pushes the due date to the next review day (one card fewer in today's queue), the response carries the queue rebuilt over the same scope, and a cross-deck scope does not degrade.
func TestReviewBuryDefersCardAndKeepsScope(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deckA := seedReviewDeck(t, db, ownerID, "Bury deck A")
	deckB := seedReviewDeck(t, db, ownerID, "Bury deck B")
	noteA := seedBasic(t, db, deckA.ID, "A front", "A back")
	seedBasic(t, db, deckB.ID, "B front", "B back")
	cardA := cardIDOfNote(t, db, noteA.ID)
	cardAPub := cardPublicIDOfNote(t, db, noteA.ID)

	// Multi-deck scope: after burying card A the queue should hold only card B, and the scope is still A∪B.
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{
		"card_id": cardAPub, "deck": []string{deckA.PublicID, deckB.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("bury = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body queueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bury response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.Remaining != 1 || len(body.Cards) != 1 {
		t.Fatalf("after bury remaining=%d cards=%d, want 1/1 (body %s)", body.Remaining, len(body.Cards), snippet(rec.Body.String()))
	}
	if body.Cards[0].DeckID != deckB.PublicID {
		t.Errorf("after bury the surviving card is from deck %s, want deck B %s", body.Cards[0].DeckID, deckB.PublicID)
	}

	// Bury pushes the due date to the next review day: the card's state row due_at must be later than now.
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardA, ownerID).First(&st).Error; err != nil {
		t.Fatalf("load buried card state: %v", err)
	}
	if st.DueAt == nil || !st.DueAt.After(time.Now().UTC()) {
		t.Fatalf("buried due_at = %v, want a future timestamp", st.DueAt)
	}

	// Fetching the queue again over the same scope still leaves only card B (the scope did not degrade).
	due := getWithCookies(t, srv, "/api/v1/review/due?deck="+deckA.PublicID+"&deck="+deckB.PublicID, cookies)
	if due.Code != http.StatusOK {
		t.Fatalf("GET due after bury = %d, want 200 (body %s)", due.Code, snippet(due.Body.String()))
	}
	var dueBody struct {
		Cards []struct {
			DeckID string `json:"deck_id"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(due.Body.Bytes(), &dueBody); err != nil {
		t.Fatal(err)
	}
	if len(dueBody.Cards) != 1 || dueBody.Cards[0].DeckID != deckB.PublicID {
		t.Errorf("due after bury = %+v, want only deck B %s", dueBody.Cards, deckB.PublicID)
	}
}

// TestReviewRenderReturnsStoredHTML asserts the card render endpoint returns the stored field HTML unchanged: the server
// no longer sanitises (the sandboxed frame is the security boundary), edit_href points at the card's edit page, and
// missing session/CSRF and an out-of-scope request are all rejected.
func TestReviewRenderReturnsSanitizedHTML(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Render deck")
	note := seedBasic(t, db, deck.ID,
		"<script>alert(1)</script> front-text",
		"<img src=x onerror=alert(1)> back-text")
	cardPub := cardPublicIDOfNote(t, db, note.ID)

	// Rejection paths.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without session = %d, want 403", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Errorf("render without CSRF = %d, want 403", rec.Code)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body reviewRenderBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if body.FrontHTML == "" || body.BackHTML == "" {
		t.Fatalf("render returned empty HTML: %+v", body)
	}
	// The stored markup is returned as it is stored. Sanitising here would be a second, divergent rule: the frame is
	// sandboxed and is the only place the HTML is ever executed.
	if !strings.Contains(body.FrontHTML, "<script>alert(1)</script> front-text") {
		t.Errorf("render front HTML is not the stored HTML: %s", body.FrontHTML)
	}
	if !strings.Contains(body.BackHTML, `<img src=x onerror=alert(1)> back-text`) {
		t.Errorf("render back HTML is not the stored HTML: %s", body.BackHTML)
	}
	if body.EditHref != "/decks/"+deck.PublicID+"/notes/"+note.PublicID {
		t.Errorf("render edit_href = %q, want the note edit path", body.EditHref)
	}
	// Next interval of the four grades: a new card follows the default learning steps 1m,10m, Again is 1 minute, and the rest do not step down grade by grade.
	if len(body.Intervals) != 4 {
		t.Fatalf("render intervals = %+v, want 4 entries", body.Intervals)
	}
	for i, iv := range body.Intervals {
		if iv.Rating != i+1 {
			t.Errorf("render intervals[%d].rating = %d, want %d", i, iv.Rating, i+1)
		}
		if i > 0 && iv.Seconds < body.Intervals[i-1].Seconds {
			t.Errorf("render intervals not ordered: %+v", body.Intervals)
		}
	}
	if body.Intervals[0].Seconds != 60 {
		t.Errorf("render Again interval = %ds, want 60s", body.Intervals[0].Seconds)
	}
}

// TestReviewRenderOmitsChoiceOptionsFromFront asserts the review card front of a choice question holds only the stem: the options are rendered by the review page
// as answer controls, so concatenating them into front_html again would show them twice on the page. The back still gives the correct options.
func TestReviewRenderOmitsChoiceOptionsFromFront(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Choice render deck")
	cases := []struct {
		name     string
		kind     string
		fields   map[string]any
		question string
		absent   []string
		wantBack string
	}{
		{"single", "choice_single", map[string]any{"question": "pick-question", "options": []string{"opt-alpha", "opt-beta", "opt-gamma"}, "answer": 1}, "pick-question", []string{"opt-alpha", "opt-beta", "opt-gamma"}, "opt-beta"},
		{"multi", "choice_multi", map[string]any{"question": "multi-question", "options": []string{"opt-one", "opt-two", "opt-three"}, "answers": []any{0, 2}}, "multi-question", []string{"opt-one", "opt-two", "opt-three"}, "opt-three"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note := seedGradedNote(t, db, deck.ID, tc.kind, tc.fields)
			rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
				"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID},
			}, cookies, csrf)
			if rec.Code != http.StatusOK {
				t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			var body reviewRenderBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
			}
			if !strings.Contains(body.FrontHTML, tc.question) {
				t.Errorf("front_html lost the question: %s", body.FrontHTML)
			}
			for _, option := range tc.absent {
				if strings.Contains(body.FrontHTML, option) {
					t.Errorf("front_html repeats option %q that the answer controls already show: %s", option, body.FrontHTML)
				}
			}
			if !strings.Contains(body.BackHTML, tc.wantBack) {
				t.Errorf("back_html = %s, want it to contain the correct option %q", body.BackHTML, tc.wantBack)
			}
		})
	}
}

// TestReviewRenderDeckFieldsFollowTheSubdeckPath asserts {{Deck}} and {{Subdeck}} render from the card's stored subdeck
// path: {{Deck}} is the deck name joined with deck_path, and {{Subdeck}} is the last "::"-separated component of that
// name. A card whose deck_path is null renders both fields as the deck name, and an empty deck_path is treated the same.
func TestReviewRenderDeckFieldsFollowTheSubdeckPath(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Path deck")
	// The stock basic template does not reference the special fields, so the test declares its own note type.
	cfg := notetype.Config{
		Kind:   notetype.KindNormal,
		Fields: []notetype.Field{{ID: "front", Name: "Front"}},
		Templates: []notetype.Template{{
			ID:      "forward",
			Name:    "Card 1",
			QFormat: "{{Front}} deck={{Deck}} sub={{Subdeck}}",
			AFormat: "{{Front}} deck={{Deck}} sub={{Subdeck}}",
		}},
	}
	noteType := &store.NoteType{DeckID: deck.ID, Name: "Deck fields"}
	if err := store.NewNoteTypeStore(db).Create(t.Context(), noteType, cfg); err != nil {
		t.Fatalf("create note type: %v", err)
	}
	seedNote := func(front string) *store.Note {
		t.Helper()
		note := &store.Note{DeckID: deck.ID, NoteTypeID: store.Ptr(noteType.ID)}
		if _, err := store.NewNoteStore(db).Create(t.Context(), note, map[string]any{"front": front}); err != nil {
			t.Fatalf("create note %q: %v", front, err)
		}
		return note
	}
	nested := seedNote("nested-question")
	plain := seedNote("plain-question")
	emptyPath := seedNote("empty-path-question")
	// The import writes deck_path on the card, not on the note, so the fixture sets it on the card row directly.
	if err := db.Model(&store.Card{}).Where("note_id = ?", nested.ID).
		Update("deck_path", "Lesson 1::Verbs").Error; err != nil {
		t.Fatalf("set deck_path: %v", err)
	}
	// An empty string carries no path component, so it is treated as a null path.
	if err := db.Model(&store.Card{}).Where("note_id = ?", emptyPath.ID).
		Update("deck_path", "").Error; err != nil {
		t.Fatalf("set empty deck_path: %v", err)
	}

	render := func(noteID uint64) string {
		t.Helper()
		rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
			"card_id": cardPublicIDOfNote(t, db, noteID), "deck": []string{deck.PublicID},
		}, cookies, csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
		}
		var body reviewRenderBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
		}
		return body.FrontHTML
	}

	if front := render(nested.ID); !strings.Contains(front, "deck=Path deck::Lesson 1::Verbs sub=Verbs") {
		t.Errorf("nested front_html = %q, want {{Deck}} = the deck name plus deck_path and {{Subdeck}} = the last component", front)
	}
	if front := render(plain.ID); !strings.Contains(front, "deck=Path deck sub=Path deck") {
		t.Errorf("front_html without deck_path = %q, want both fields = the deck name", front)
	}
	if front := render(emptyPath.ID); !strings.Contains(front, "deck=Path deck sub=Path deck") {
		t.Errorf("front_html with an empty deck_path = %q, want both fields = the deck name", front)
	}
}

// TestReviewRenderReturnsOptionHTML asserts the render response carries one stored HTML fragment per choice option, in
// option order: the answer controls inject these fragments, and a formula option keeps its TeX delimiters for
// typesetting. It also asserts a card type without options returns JSON null, not an empty array.
func TestReviewRenderReturnsOptionHTML(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Choice option HTML deck")
	note := seedGradedNote(t, db, deck.ID, "choice_single", map[string]any{
		"question": "pick-question",
		"options":  []string{`\(x^2\)`, "opt-beta", "<script>alert(1)</script>"},
		"answer":   0,
	})
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body reviewRenderBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	if len(body.OptionsHTML) != 3 {
		t.Fatalf("options_html = %#v, want one entry per option", body.OptionsHTML)
	}
	if !strings.Contains(body.OptionsHTML[0], `\(x^2\)`) {
		t.Errorf("option 0 lost its TeX delimiters, MathJax cannot typeset it: %s", body.OptionsHTML[0])
	}
	if body.OptionsHTML[1] != "opt-beta" {
		t.Errorf("option 1 = %q, want the stored option in option order", body.OptionsHTML[1])
	}
	if body.OptionsHTML[2] != "<script>alert(1)</script>" {
		t.Errorf("option 2 = %q, want the stored HTML (the server does not sanitise)", body.OptionsHTML[2])
	}
	for _, option := range []string{`\(x^2\)`, "opt-beta"} {
		if strings.Contains(body.FrontHTML, option) {
			t.Errorf("front_html repeats option %q that the answer controls already show: %s", option, body.FrontHTML)
		}
	}

	basic := seedBasic(t, db, deck.ID, "basic-front", "basic-back")
	rec = postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, basic.ID), "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("basic render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode basic render response: %v", err)
	}
	if got := string(raw["options_html"]); got != "null" {
		t.Errorf("non-choice options_html = %s, want null", got)
	}
}

// TestReviewRenderReturnsClozeBlanks asserts the cloze card's render response gives the hint for each blank of the card in order of appearance
// (the review page adds one input box per blank from it), and the front does not leak the answer; other question types have no blanks.
func TestReviewRenderReturnsClozeBlanks(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Cloze render deck")
	cloze := seedGradedNote(t, db, deck.ID, "cloze", map[string]any{"text": "{{c1::Paris}} and {{c1::Rome::city}} are capitals"})
	basic := seedBasic(t, db, deck.ID, "Q", "A")

	render := func(noteID uint64) map[string]any {
		t.Helper()
		rec := postJSONWithCSRF(t, srv, "/api/v1/review/render", map[string]any{
			"card_id": cardPublicIDOfNote(t, db, noteID), "deck": []string{deck.PublicID},
		}, cookies, csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode render response: %v", err)
		}
		return body
	}

	body := render(cloze.ID)
	blanks, ok := body["blanks"].([]any)
	if !ok || len(blanks) != 2 || blanks[0] != "" || blanks[1] != "city" {
		t.Errorf("cloze blanks = %#v, want [\"\", \"city\"]", body["blanks"])
	}
	// The visible text of the active deletion is masked. Its content stays in the data-cloze attribute, which is the
	// engine's markup contract for animating the answer back in.
	if front, _ := body["front_html"].(string); !strings.Contains(front, `data-cloze="Paris"`) {
		t.Errorf("cloze front_html = %s, want the active deletion carried as data-cloze", front)
	}
	if got := render(basic.ID)["blanks"]; got != nil {
		t.Errorf("basic blanks = %#v, want null", got)
	}
}

// TestReviewSuspendRemovesCardForCallerOnly covers the review page's suspend endpoint: a missing CSRF is rejected and nothing is written (negative case);
// after suspending, the queue in the response no longer holds this card, the state row carries suspended_at; the progress numbers are unchanged.
func TestReviewSuspendRemovesCardForCallerOnly(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Suspend deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	seedBasic(t, db, deck.ID, "Q2", "A2")
	cardID := cardIDOfNote(t, db, note.ID)
	cardPub := cardPublicIDOfNote(t, db, note.ID)
	req := map[string]any{"card_id": cardPub, "deck": []string{deck.PublicID}}

	if rec := postJSONWithCSRF(t, srv, "/api/v1/review/suspend", req, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("suspend without CSRF = %d, want 403", rec.Code)
	}
	var states int64
	db.Model(&store.CardState{}).Count(&states)
	if states != 0 {
		t.Fatalf("denied suspend wrote %d card_states rows, want 0", states)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/review/suspend", req, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body queueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode suspend response: %v", err)
	}
	for _, c := range body.Cards {
		if c.CardID == cardPub {
			t.Fatalf("suspended card %s is still in the returned queue", cardPub)
		}
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, ownerID).First(&st).Error; err != nil {
		t.Fatalf("load suspended state: %v", err)
	}
	if st.SuspendedAt == nil || st.State != "new" {
		t.Errorf("state after suspend = %+v, want suspended_at set on a new-card row", st)
	}
}

// TestReviewBuryUsesTheUsersReviewDay asserts bury pushes to the start of "the user's own" next review day: the due moment is exactly the day cutoff
// in the user's timezone (default 04:00). Negative case: computed in UTC, a user at UTC+14 would get 18:00 local.
func TestReviewBuryUsesTheUsersReviewDay(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	const tz = "Pacific/Kiritimati"
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("timezone", tz).Error; err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	deck := seedReviewDeck(t, db, ownerID, "Bury tz deck")
	note := seedBasic(t, db, deck.ID, "Q", "A")
	rec := postJSONWithCSRF(t, srv, "/api/v1/review/bury", map[string]any{
		"card_id": cardPublicIDOfNote(t, db, note.ID), "deck": []string{deck.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("bury = %d (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var st store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardIDOfNote(t, db, note.ID), ownerID).First(&st).Error; err != nil {
		t.Fatalf("load state: %v", err)
	}
	local := st.DueAt.In(loc)
	if local.Hour() != reviewday.DefaultCutoffHour || local.Minute() != 0 {
		t.Errorf("buried until %s local, want the next %02d:00 in %s", local.Format(time.RFC3339), reviewday.DefaultCutoffHour, tz)
	}
}

// TestReviewRenderEmptyCardShowsLocalizedNotice is the acceptance case for a card the note no
// longer generates after a note write: the review front is replaced by a notice, and the notice
// text follows the request locale. The card itself stays in the response and in the queue.
func TestReviewRenderEmptyCardShowsLocalizedNotice(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// The account default locale is zh-CN, and a user setting outranks Accept-Language. Clearing it
	// is what lets the request header choose the locale under test.
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("locale", "").Error; err != nil {
		t.Fatalf("clear the user locale: %v", err)
	}
	deck := seedReviewDeck(t, db, ownerID, "Empty card deck")
	note := seedGradedNote(t, db, deck.ID, "basic_both", map[string]any{"front": "question", "back": "answer"})

	// Clearing the back field makes the reverse template render an empty front, so the note no
	// longer generates that card. The note write keeps it.
	if _, err := store.NewNoteStore(db).Update(context.Background(),
		&store.Note{ID: note.ID, DeckID: deck.ID, NoteTypeID: note.NoteTypeID},
		map[string]any{"front": "question", "back": ""}); err != nil {
		t.Fatalf("clear the reverse field: %v", err)
	}
	var reverse store.Card
	if err := db.Where("note_id = ? AND template = ?", note.ID, "reverse").First(&reverse).Error; err != nil {
		t.Fatalf("load reverse card: %v", err)
	}

	cases := []struct {
		name    string
		lang    string
		notices []string
		other   string
	}{
		{
			name:    "en",
			lang:    "en-US,en;q=0.9",
			notices: []string{"This card is empty", "The Empty Cards cleanup removes it"},
			other:   "这张卡片是空的",
		},
		{
			name:    "zh-CN",
			lang:    "zh-CN,zh;q=0.9",
			notices: []string{"这张卡片是空的", "清理空卡片会将它删除"},
			other:   "This card is empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postJSON(srv, "/api/v1/review/render", map[string]any{
				"card_id": reverse.PublicID, "deck": []string{deck.PublicID},
			}, cookies, map[string]string{
				auth.CSRFHeaderName: csrf,
				"Accept-Language":   tc.lang,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("render = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			var body reviewRenderBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode render response: %v (body %s)", err, snippet(rec.Body.String()))
			}
			if body.CardID != reverse.PublicID {
				t.Errorf("render card_id = %q, want the empty card %q", body.CardID, reverse.PublicID)
			}
			for _, notice := range tc.notices {
				if !strings.Contains(body.FrontHTML, notice) {
					t.Errorf("front_html = %q, want it to contain %q", body.FrontHTML, notice)
				}
			}
			if body.Front.HTML != body.FrontHTML {
				t.Errorf("front.html = %q, front_html = %q, want the same notice", body.Front.HTML, body.FrontHTML)
			}
			if strings.Contains(body.FrontHTML, tc.other) {
				t.Errorf("front_html carries the other locale's notice %q: %q", tc.other, body.FrontHTML)
			}
		})
	}
}
