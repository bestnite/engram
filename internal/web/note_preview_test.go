package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// postPreview sends one preview body with the owner's session and CSRF token.
func postPreview(t *testing.T, srv *Server, deckPublicID string, cookies []*http.Cookie, csrf string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode preview request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deckPublicID+"/notes/preview", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestNotePreviewReturnsHTMLAndPreservesTeX(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview")
	noteTypeID := stockNoteTypePublicID(t, db, deck.ID, "basic")
	rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{NoteType: noteTypeID, Fields: map[string]any{
		"front": `<img src=x onerror=alert(1)> \(x^2\)`, "back": `<script>alert(1)</script>safe`,
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
			BackHTML  string `json:"back_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || len(preview.Cards) != 1 {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	// The preview shows the stored HTML unchanged: the server does not sanitise, and the frame sandbox is the boundary.
	if !strings.Contains(preview.Cards[0].FrontHTML, "onerror") || !strings.Contains(preview.Cards[0].BackHTML, "<script") {
		t.Fatalf("preview did not return the stored HTML: %+v", preview.Cards[0])
	}
	if !strings.Contains(preview.Cards[0].FrontHTML, `\(x^2\)`) || !strings.Contains(preview.Cards[0].BackHTML, "safe") {
		t.Fatalf("TeX or content missing: %+v", preview.Cards[0])
	}
}

// TestNotePreviewKeepsChoiceOptionsOutOfTheFace asserts the preview face carries the question only: choice options belong to
// the answer control's markup, which is drawn by the frame, so the face must not repeat them.
func TestNotePreviewKeepsChoiceOptionsInline(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview choices")
	noteTypeID := stockNoteTypePublicID(t, db, deck.ID, "choice_single")
	rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{NoteType: noteTypeID, Fields: map[string]any{
		"question": "pick-one", "options": []any{"alpha", "beta"}, "answer": 1,
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
			BackHTML  string `json:"back_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Cards) != 1 {
		t.Fatalf("preview cards = %d, want 1 (body %s)", len(body.Cards), rec.Body.String())
	}
	front := body.Cards[0].FrontHTML
	if !strings.Contains(front, "pick-one") {
		t.Fatalf("preview front is missing the question: %s", front)
	}
	for _, option := range []string{"alpha", "beta"} {
		if strings.Contains(front, option) {
			t.Errorf("preview front repeats option %q that the answer control draws: %s", option, front)
		}
	}
	if !strings.Contains(body.Cards[0].BackHTML, "beta") {
		t.Errorf("preview back = %s, want the correct option", body.Cards[0].BackHTML)
	}
}

// TestNotePreviewRendersStoredNoteType is the acceptance case for the preview endpoint: a note type whose template and CSS
// differ from every stock definition renders its own stored template, not the stock definition of a kind.
func TestNotePreviewRendersStoredNoteType(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview stored type")
	cfg := notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-question", Name: "Question"},
			{ID: "f-answer", Name: "Answer"},
		},
		Templates: []notetype.Template{{
			ID:      "t-stored",
			Name:    "Stored card",
			QFormat: `<div id="stored-question">{{Question}}</div>`,
			AFormat: `<div id="stored-question">{{Question}}</div><div id="stored-answer">{{Answer}}</div>`,
		}},
		CSS: `.card { color: rgb(9, 9, 9); }`,
	}
	nt := &store.NoteType{DeckID: deck.ID, Name: "Stored layout"}
	if err := store.NewNoteTypeStore(db).Create(t.Context(), nt, cfg); err != nil {
		t.Fatalf("create note type: %v", err)
	}
	rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{NoteType: nt.PublicID, Fields: map[string]any{
		"Question": "STORED-QUESTION", "Answer": "STORED-ANSWER",
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Cards []struct {
			Front struct {
				HTML string `json:"html"`
				CSS  string `json:"css"`
			} `json:"front"`
			Back struct {
				HTML string `json:"html"`
				CSS  string `json:"css"`
			} `json:"back"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Cards) != 1 {
		t.Fatalf("preview cards = %d, want 1 (body %s)", len(body.Cards), rec.Body.String())
	}
	if !strings.Contains(body.Cards[0].Front.HTML, `id="stored-question"`) || !strings.Contains(body.Cards[0].Front.HTML, "STORED-QUESTION") {
		t.Errorf("preview front does not render the stored template: %s", body.Cards[0].Front.HTML)
	}
	if !strings.Contains(body.Cards[0].Back.HTML, `id="stored-answer"`) || !strings.Contains(body.Cards[0].Back.HTML, "STORED-ANSWER") {
		t.Errorf("preview back does not render the stored template: %s", body.Cards[0].Back.HTML)
	}
	if body.Cards[0].Front.CSS != cfg.CSS || body.Cards[0].Back.CSS != cfg.CSS {
		t.Errorf("preview css = %q / %q, want the stored %q", body.Cards[0].Front.CSS, body.Cards[0].Back.CSS, cfg.CSS)
	}
}

// TestNotePreviewRejectsNoteTypeOfAnotherDeck covers the negative case: the note type must belong to the deck in the address,
// so a preview cannot read a type of a deck the caller did not address.
func TestNotePreviewRejectsNoteTypeOfAnotherDeck(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview target")
	other := seedDeck(t, db, ownerID, "Preview other")
	foreignTypeID := stockNoteTypePublicID(t, db, other.ID, "basic")
	rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{NoteType: foreignTypeID, Fields: map[string]any{
		"front": "Q", "back": "A",
	}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preview of another deck's note type = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// postNoteTypePreview sends one unsaved-config preview with the given session and CSRF token.
func postNoteTypePreview(t *testing.T, srv *Server, deckPublicID string, cookies []*http.Cookie, csrf string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode note type preview request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deckPublicID+"/note-types/preview", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestNoteTypePreviewRendersClozeMarkup is the acceptance case for an unsaved cloze config: the preview runs the
// server's cloze filter, so the frame sees the blank markup instead of the field name alone.
func TestNoteTypePreviewRendersClozeMarkup(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Unsaved cloze preview")
	cfg := notetype.Config{
		Kind:   notetype.KindCloze,
		Fields: []notetype.Field{{ID: "f-text", Name: "Text"}},
		Templates: []notetype.Template{{
			ID:      "t-cloze",
			Name:    "Cloze",
			QFormat: "{{cloze:Text}}",
			AFormat: "{{cloze:Text}}",
		}},
		CSS: `.card { color: rgb(3, 3, 3); }`,
	}
	rec := postNoteTypePreview(t, srv, deck.PublicID, cookies, csrf, noteTypePreviewRequest{Config: &cfg, TemplateID: "t-cloze"})
	if rec.Code != http.StatusOK {
		t.Fatalf("note type preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Front struct {
			HTML string `json:"html"`
			CSS  string `json:"css"`
			Side string `json:"side"`
		} `json:"front"`
		Back struct {
			HTML string `json:"html"`
			Side string `json:"side"`
		} `json:"back"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode note type preview response: %v (body %s)", err, rec.Body.String())
	}
	if !strings.Contains(body.Front.HTML, `class="cloze"`) || !strings.Contains(body.Front.HTML, `data-cloze="Text"`) {
		t.Errorf("cloze preview front = %q, want the hidden cloze markup", body.Front.HTML)
	}
	if !strings.Contains(body.Back.HTML, `class="cloze"`) || !strings.Contains(body.Back.HTML, ">Text</span>") {
		t.Errorf("cloze preview back = %q, want the revealed cloze markup", body.Back.HTML)
	}
	if body.Front.CSS != cfg.CSS {
		t.Errorf("cloze preview css = %q, want the unsaved %q", body.Front.CSS, cfg.CSS)
	}
	if body.Front.Side != notetype.SideFront.String() || body.Back.Side != notetype.SideBack.String() {
		t.Errorf("preview sides = %q / %q, want front / back", body.Front.Side, body.Back.Side)
	}
}

// TestNoteTypePreviewRejectsInvalidConfig covers the negative case: the same validation rule a stored note type runs
// rejects the draft, and the response names the offending template.
func TestNoteTypePreviewRejectsInvalidConfig(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Invalid unsaved preview")
	cfg := notetype.Config{
		Kind:   notetype.KindNormal,
		Fields: []notetype.Field{{ID: "f-front", Name: "Front"}},
		Templates: []notetype.Template{{
			ID:      "t-broken",
			Name:    "Broken card",
			QFormat: "{{Missing}}",
			AFormat: "{{Front}}",
		}},
	}
	rec := postNoteTypePreview(t, srv, deck.PublicID, cookies, csrf, noteTypePreviewRequest{Config: &cfg, TemplateID: "t-broken"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid config preview status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Broken card") {
		t.Errorf("invalid config response does not name the template: %s", rec.Body.String())
	}
}

// TestNoteTypePreviewRequiresEditor covers the role gate: a reader of the deck may not render a draft through the
// preview, because the endpoint belongs to the note type editor.
func TestNoteTypePreviewRequiresEditor(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Reader preview")
	readerID, readerCookies, readerCSRF := createUserAndLogin(t, srv, db, "preview-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	cfg := notetype.Config{
		Kind:   notetype.KindNormal,
		Fields: []notetype.Field{{ID: "f-front", Name: "Front"}},
		Templates: []notetype.Template{{
			ID: "t-one", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Front}}",
		}},
	}
	rec := postNoteTypePreview(t, srv, deck.PublicID, readerCookies, readerCSRF, noteTypePreviewRequest{Config: &cfg, TemplateID: "t-one"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader preview status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestNotePreviewRequiresSessionAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview")
	payload, _ := json.Marshal(notePreviewRequest{NoteType: stockNoteTypePublicID(t, db, deck.ID, "basic"), Fields: map[string]any{"front": "a", "back": "b"}})
	request := func(withCookie, withCSRF bool) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		if withCookie {
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	if got := request(false, false); got != http.StatusForbidden {
		t.Errorf("anonymous status = %d, want 403", got)
	}
	if got := request(true, false); got != http.StatusForbidden {
		t.Errorf("missing CSRF status = %d, want 403", got)
	}
}
