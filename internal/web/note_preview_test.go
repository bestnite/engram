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
			Front CardFace `json:"front"`
			Back  CardFace `json:"back"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || len(preview.Cards) != 1 {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	// The preview shows the stored HTML unchanged: the server does not sanitise, and the frame sandbox is the boundary.
	if !strings.Contains(preview.Cards[0].Front.HTML, "onerror") || !strings.Contains(preview.Cards[0].Back.HTML, "<script") {
		t.Fatalf("preview did not return the stored HTML: %+v", preview.Cards[0])
	}
	if !strings.Contains(preview.Cards[0].Front.HTML, `\(x^2\)`) || !strings.Contains(preview.Cards[0].Back.HTML, "safe") {
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
			Front CardFace `json:"front"`
			Back  CardFace `json:"back"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Cards) != 1 {
		t.Fatalf("preview cards = %d, want 1 (body %s)", len(body.Cards), rec.Body.String())
	}
	front := body.Cards[0].Front.HTML
	if !strings.Contains(front, "pick-one") {
		t.Fatalf("preview front is missing the question: %s", front)
	}
	for _, option := range []string{"alpha", "beta"} {
		if strings.Contains(front, option) {
			t.Errorf("preview front repeats option %q that the answer control draws: %s", option, front)
		}
	}
	if !strings.Contains(body.Cards[0].Back.HTML, "beta") {
		t.Errorf("preview back = %s, want the correct option", body.Cards[0].Back.HTML)
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

// TestNotePreviewNamesEachCard asserts every preview card carries the template that produced it and, for a cloze note,
// its cloze number: the editor pages through the cards one at a time and names the card it shows.
func TestNotePreviewNamesEachCard(t *testing.T) {
	type cardName struct {
		Template     string `json:"template"`
		ClozeOrdinal int    `json:"cloze_ordinal"`
	}
	cases := []struct {
		name   string
		cfg    notetype.Config
		fields map[string]any
		want   []cardName
	}{
		{
			name: "normal templates in order",
			cfg: notetype.Config{
				Kind:   notetype.KindNormal,
				Fields: []notetype.Field{{ID: "f-front", Name: "Front"}, {ID: "f-back", Name: "Back"}},
				Templates: []notetype.Template{
					{ID: "t-forward", Name: "Forward", QFormat: "{{Front}}", AFormat: "{{Back}}"},
					{ID: "t-reverse", Name: "Reverse", QFormat: "{{Back}}", AFormat: "{{Front}}"},
				},
			},
			fields: map[string]any{"Front": "front", "Back": "back"},
			want:   []cardName{{Template: "Forward"}, {Template: "Reverse"}},
		},
		{
			name: "cloze numbers in order",
			cfg: notetype.Config{
				Kind:   notetype.KindCloze,
				Fields: []notetype.Field{{ID: "f-text", Name: "Text"}},
				Templates: []notetype.Template{{
					ID: "t-cloze", Name: "Cloze", QFormat: "{{cloze:Text}}", AFormat: "{{cloze:Text}}",
				}},
			},
			fields: map[string]any{"Text": "{{c3::three}} and {{c1::one}}"},
			want:   []cardName{{Template: "Cloze", ClozeOrdinal: 1}, {Template: "Cloze", ClozeOrdinal: 3}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedDeck(t, db, ownerID, "Card names")
			nt := &store.NoteType{DeckID: deck.ID, Name: "Named cards"}
			if err := store.NewNoteTypeStore(db).Create(t.Context(), nt, tc.cfg); err != nil {
				t.Fatalf("create note type: %v", err)
			}
			rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{NoteType: nt.PublicID, Fields: tc.fields})
			if rec.Code != http.StatusOK {
				t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var body struct {
				Cards []cardName `json:"cards"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode preview response: %v", err)
			}
			if len(body.Cards) != len(tc.want) {
				t.Fatalf("cards = %+v, want %+v", body.Cards, tc.want)
			}
			for i := range tc.want {
				if body.Cards[i] != tc.want[i] {
					t.Errorf("card %d = %+v, want %+v", i, body.Cards[i], tc.want[i])
				}
			}
		})
	}
}

// draftPreviewConfig is an unsaved note type whose second card exists only when Add Reverse is filled, and whose answer
// shows the tags, so one config covers card generation and {{Tags}}.
func draftPreviewConfig() *notetype.Config {
	return &notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f0", Name: "Front"}, {ID: "f1", Name: "Back"}, {ID: "f2", Name: "Add Reverse"},
		},
		Templates: []notetype.Template{
			{ID: "t0", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}<hr id=answer>{{Back}}<p class=tags>{{Tags}}</p>"},
			{ID: "t1", Name: "Card 2", QFormat: "{{#Add Reverse}}{{Back}}{{/Add Reverse}}", AFormat: "{{FrontSide}}<hr id=answer>{{Front}}"},
		},
	}
}

// TestNotePreviewRendersUnsavedConfig covers the config mode the note type help uses: the sample values are rendered, the
// cards are the ones a real note would generate, and the supplied tags fill {{Tags}}.
func TestNotePreviewRendersUnsavedConfig(t *testing.T) {
	type face struct {
		HTML string `json:"html"`
	}
	type card struct {
		Template string `json:"template"`
		Front    face   `json:"front"`
		Back     face   `json:"back"`
	}
	cases := []struct {
		name      string
		fields    map[string]any
		tags      []string
		templates []string
		backHas   string
	}{
		{"conditional card left out", map[string]any{"Front": "SAMPLE-FRONT", "Back": "SAMPLE-BACK"}, []string{" physics ", "", "heat"}, []string{"Card 1"}, "physics heat"},
		{"conditional card generated", map[string]any{"Front": "SAMPLE-FRONT", "Back": "SAMPLE-BACK", "Add Reverse": "y"}, nil, []string{"Card 1", "Card 2"}, "SAMPLE-BACK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedDeck(t, db, ownerID, "Unsaved preview")
			rec := postPreview(t, srv, deck.PublicID, cookies, csrf, notePreviewRequest{Config: draftPreviewConfig(), Fields: tc.fields, Tags: tc.tags})
			if rec.Code != http.StatusOK {
				t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var body struct {
				Cards []card `json:"cards"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode preview response: %v", err)
			}
			var names []string
			for _, c := range body.Cards {
				names = append(names, c.Template)
			}
			if strings.Join(names, ",") != strings.Join(tc.templates, ",") {
				t.Fatalf("cards = %v, want %v", names, tc.templates)
			}
			if !strings.Contains(body.Cards[0].Front.HTML, "SAMPLE-FRONT") {
				t.Errorf("front = %q, want the sample value", body.Cards[0].Front.HTML)
			}
			if !strings.Contains(body.Cards[0].Back.HTML, tc.backHas) {
				t.Errorf("back = %q, want it to contain %q", body.Cards[0].Back.HTML, tc.backHas)
			}
		})
	}
}

// TestNotePreviewRejectsAmbiguousOrInvalidType covers the negative cases of the type selection: naming both a stored type
// and a config, naming neither, sending a config that fails note type validation, and a field the config does not declare.
func TestNotePreviewRejectsAmbiguousOrInvalidType(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Ambiguous preview")
	nt := &store.NoteType{DeckID: deck.ID, Name: "Stored"}
	if err := store.NewNoteTypeStore(db).Create(t.Context(), nt, *draftPreviewConfig()); err != nil {
		t.Fatalf("create note type: %v", err)
	}
	broken := draftPreviewConfig()
	broken.Templates[0].QFormat = "{{Missing}}"
	fields := map[string]any{"Front": "a", "Back": "b"}
	cases := []struct {
		name string
		req  notePreviewRequest
	}{
		{"both a stored type and a config", notePreviewRequest{NoteType: nt.PublicID, Config: draftPreviewConfig(), Fields: fields}},
		{"neither a stored type nor a config", notePreviewRequest{Fields: fields}},
		{"invalid config", notePreviewRequest{Config: broken, Fields: fields}},
		{"undeclared field", notePreviewRequest{Config: draftPreviewConfig(), Fields: map[string]any{"Front": "a", "Nope": "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postPreview(t, srv, deck.PublicID, cookies, csrf, tc.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestNotePreviewUnsavedConfigRequiresEditor covers the role gate of the config mode: a reader may not render through the
// preview, exactly as with a stored type.
func TestNotePreviewUnsavedConfigRequiresEditor(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Reader unsaved preview")
	readerID, readerCookies, readerCSRF := createUserAndLogin(t, srv, db, "unsaved-preview-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	rec := postPreview(t, srv, deck.PublicID, readerCookies, readerCSRF, notePreviewRequest{
		Config: draftPreviewConfig(), Fields: map[string]any{"Front": "a", "Back": "b"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader preview status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestNotePreviewDrawsAnswerControlForFrame pins the front a frame preview shows: the answer control's input in place of
// the type marker, as review draws it, both for a registered control and for an Anki type-in field. A request that is
// not for the frame keeps the marker stripped (TestNotePreviewKeepsChoiceOptionsInline).
func TestNotePreviewDrawsAnswerControlForFrame(t *testing.T) {
	typeIn := &notetype.Config{
		Kind:   notetype.KindNormal,
		Fields: []notetype.Field{{ID: "f0", Name: "Front"}, {ID: "f1", Name: "Back"}},
		Templates: []notetype.Template{{
			ID: "t0", Name: "Card 1", QFormat: "{{Front}}{{type:Back}}", AFormat: "{{Front}}<hr id=answer>{{type:Back}}",
		}},
	}
	cases := []struct {
		name     string
		req      func(deck *store.Deck, db func() string) notePreviewRequest
		frontHas []string
	}{
		{
			name: "registered choice control",
			req: func(_ *store.Deck, choiceType func() string) notePreviewRequest {
				return notePreviewRequest{NoteType: choiceType(), Fields: map[string]any{
					"question": "pick-one", "options": []any{"alpha", "beta"}, "answer": 1,
				}}
			},
			frontHas: []string{`type="radio"`, "alpha", "beta"},
		},
		{
			name: "Anki type-in field",
			req: func(*store.Deck, func() string) notePreviewRequest {
				return notePreviewRequest{Config: typeIn, Fields: map[string]any{"Front": "thanks", "Back": "thank you"}}
			},
			frontHas: []string{`id="typeans"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedDeck(t, db, ownerID, "Frame preview controls")
			choiceType := func() string { return stockNoteTypePublicID(t, db, deck.ID, "choice_single") }
			payload, err := json.Marshal(tc.req(deck, choiceType))
			if err != nil {
				t.Fatalf("encode preview request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/notes/preview", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set(frameOutputHeader, "1")
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("preview status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
			var body struct {
				Cards []struct {
					Front CardFace `json:"front"`
				} `json:"cards"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Cards) != 1 {
				t.Fatalf("decode preview response: %v (body %s)", err, rec.Body.String())
			}
			front := body.Cards[0].Front.HTML
			for _, want := range tc.frontHas {
				if !strings.Contains(front, want) {
					t.Errorf("front = %s, want it to contain %q", front, want)
				}
			}
			if typeMarkerPattern.MatchString(front) {
				t.Errorf("front = %s still carries a type marker", front)
			}
		})
	}
}
