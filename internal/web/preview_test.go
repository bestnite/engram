package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// Deck content preview: GET /api/v1/decks/:id/preview/cards renders one LIMIT/OFFSET page of the
// deck's visible cards with the review renderer, and GET /decks/:id/preview serves the SPA shell
// behind the reader-role check (that half is covered by the deck-scoped page-route table).

// previewCardListBody is the external shape of one preview page.
type previewCardListBody struct {
	Cards     []previewCardFace        `json:"cards"`
	Total     int64                    `json:"total"`
	Page      int                      `json:"page"`
	PerPage   int                      `json:"per_page"`
	Sort      string                   `json:"sort"`
	NoteTypes []previewNoteTypeSection `json:"note_types"`
}

// previewGet requests the preview endpoint with the given session cookie, optionally asking for
// frame-ready HTML the way the SPA does.
func previewGet(t *testing.T, srv *Server, target string, cookies []*http.Cookie, frame bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if frame {
		req.Header.Set(frameOutputHeader, "1")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// decodePreviewPage decodes a successful preview response.
func decodePreviewPage(t *testing.T, rec *httptest.ResponseRecorder) previewCardListBody {
	t.Helper()
	var body previewCardListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode preview body: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// seedTyped creates one typed-answer note of the deck, which has an answer spec and therefore
// draws its input control only in a frame request.
func seedTyped(t *testing.T, db *gorm.DB, deckID uint64, prompt, answer string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, NoteTypeID: store.Ptr(stockNoteTypeID(t, db, deckID, "typed"))}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"prompt": prompt, "answer": answer}); err != nil {
		t.Fatalf("create typed note: %v", err)
	}
	return note
}

// seedCloze creates one cloze note with two deletions, so the deck gains two cards.
func seedCloze(t *testing.T, db *gorm.DB, deckID uint64) *store.Note {
	t.Helper()
	return seedClozeText(t, db, deckID, "{{c1::water}} and {{c2::fire}}")
}

// seedClozeText creates one cloze note from the given text, so a test can choose how many cards a
// note generates.
func seedClozeText(t *testing.T, db *gorm.DB, deckID uint64, text string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, NoteTypeID: store.Ptr(stockNoteTypeID(t, db, deckID, "cloze"))}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"text": text}); err != nil {
		t.Fatalf("create cloze note: %v", err)
	}
	return note
}

// TestDeckPreviewCardsRequiresReaderRole pins the endpoint's authorization: the owner reads, an
// anonymous request gets 401, a signed-in stranger gets 403, and a granted reader gets the page.
func TestDeckPreviewCardsRequiresReaderRole(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview auth deck")
	seedBasic(t, db, deck.ID, "Q", "A")
	path := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	if rec := previewGet(t, srv, path, cookies, false); rec.Code != http.StatusOK {
		t.Fatalf("owner GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if rec := previewGet(t, srv, path, nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	strangerID, strangerCookies, _ := createUserAndLogin(t, srv, db, "preview-stranger")
	if rec := previewGet(t, srv, path, strangerCookies, false); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger GET = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	grantRole(t, srv, deck.ID, strangerID, store.RoleReader, cookies, csrf)
	if rec := previewGet(t, srv, path, strangerCookies, false); rec.Code != http.StatusOK {
		t.Fatalf("granted reader GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
}

// TestDeckPreviewCardsUnknownDeckAndEmptyDeck covers the two non-rendering outcomes: an unknown
// deck is 404, and a deck with no cards is a valid empty page with the paging defaults.
func TestDeckPreviewCardsUnknownDeckAndEmptyDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	if rec := previewGet(t, srv, "/api/v1/decks/no-such-deck/preview/cards", cookies, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown deck GET = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	deck := seedReviewDeck(t, db, ownerID, "Preview empty deck")
	page := decodePreviewPage(t, previewGet(t, srv, "/api/v1/decks/"+deck.PublicID+"/preview/cards", cookies, false))
	if page.Total != 0 || len(page.Cards) != 0 {
		t.Fatalf("empty deck = %d cards, total %d, want none", len(page.Cards), page.Total)
	}
	if page.Page != 1 || page.PerPage != store.DefaultCardPageSize || page.Sort != store.CardListSortCreated {
		t.Errorf("empty deck defaults = page %d, per_page %d, sort %q; want 1, %d, %q",
			page.Page, page.PerPage, page.Sort, store.DefaultCardPageSize, store.CardListSortCreated)
	}
}

// TestDeckPreviewCardsPagingAndBounds pins the paging contract and the malformed-parameter refusals.
func TestDeckPreviewCardsPagingAndBounds(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview paging deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")
	seedBasic(t, db, deck.ID, "Q2", "A2")
	seedBasic(t, db, deck.ID, "Q3", "A3")
	seedCloze(t, db, deck.ID)
	base := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	first := decodePreviewPage(t, previewGet(t, srv, base+"?page=1&per_page=2", cookies, false))
	if first.Total != 5 || len(first.Cards) != 2 || first.Page != 1 || first.PerPage != 2 {
		t.Fatalf("first page = %+v, want total 5, 2 cards, page 1, per_page 2", first)
	}
	second := decodePreviewPage(t, previewGet(t, srv, base+"?page=2&per_page=2", cookies, false))
	if len(second.Cards) != 2 {
		t.Fatalf("second page has %d cards, want 2", len(second.Cards))
	}
	seen := make(map[string]bool, 4)
	for _, card := range append(first.Cards, second.Cards...) {
		if seen[card.CardID] {
			t.Errorf("card %q appears on both pages", card.CardID)
		}
		seen[card.CardID] = true
	}

	capped := decodePreviewPage(t, previewGet(t, srv, base+"?per_page=1000", cookies, false))
	if capped.PerPage != store.MaxCardPageSize {
		t.Errorf("per_page 1000 = %d, want the cap %d", capped.PerPage, store.MaxCardPageSize)
	}
	past := decodePreviewPage(t, previewGet(t, srv, base+"?page=99", cookies, false))
	if len(past.Cards) != 0 || past.Total != 5 {
		t.Errorf("page past the end = %d cards, total %d, want 0 and 5", len(past.Cards), past.Total)
	}

	for _, query := range []string{"?page=0", "?page=abc", "?per_page=0", "?per_page=abc", "?sort=bogus"} {
		if rec := previewGet(t, srv, base+query, cookies, false); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400 (body %s)", query, rec.Code, snippet(rec.Body.String()))
		}
	}
}

// TestDeckPreviewCardsSortModes pins both sort orders: creation order follows the card id, while
// note type order groups the deck by note type name first.
func TestDeckPreviewCardsSortModes(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview sort deck")
	// The cloze note is created first, so creation order starts with Cloze while note type order
	// starts with Basic ("Basic" sorts before "Cloze").
	seedCloze(t, db, deck.ID)
	seedBasic(t, db, deck.ID, "Q", "A")
	base := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	created := decodePreviewPage(t, previewGet(t, srv, base+"?sort=created", cookies, false))
	if created.Sort != store.CardListSortCreated {
		t.Fatalf("sort = %q, want %q", created.Sort, store.CardListSortCreated)
	}
	if len(created.Cards) != 3 || created.Cards[0].NoteType != "Cloze" {
		t.Fatalf("created order first card = %q (%d cards), want Cloze first", created.Cards[0].NoteType, len(created.Cards))
	}
	byType := decodePreviewPage(t, previewGet(t, srv, base+"?sort=note_type", cookies, false))
	if byType.Sort != store.CardListSortNoteType {
		t.Fatalf("sort = %q, want %q", byType.Sort, store.CardListSortNoteType)
	}
	if len(byType.Cards) != 3 || byType.Cards[0].NoteType != "Basic" || byType.Cards[1].NoteType != "Cloze" {
		t.Fatalf("note type order = %+v, want Basic then Cloze", byType.Cards)
	}
}

// TestDeckPreviewCardsRenderBothFaces pins the render contract: a frame request puts the answer
// control's input markup on the front, the back carries the answer, and both faces carry the note
// type's CSS, the body class, the side marker and the same media base.
func TestDeckPreviewCardsRenderBothFaces(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview render deck")
	seedBasic(t, db, deck.ID, "Basic question", "Basic answer")
	seedTyped(t, db, deck.ID, "Type the answer", "expected")
	base := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	page := decodePreviewPage(t, previewGet(t, srv, base+"?sort=note_type", cookies, true))
	if len(page.Cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(page.Cards))
	}
	var typed, basic previewCardFace
	for _, card := range page.Cards {
		switch card.NoteType {
		case "Typed answer":
			typed = card
		case "Basic":
			basic = card
		}
	}
	if typed.CardID == "" || basic.CardID == "" {
		t.Fatalf("page does not carry both note types: %+v", page.Cards)
	}
	if typed.TypeIn || !strings.Contains(typed.Front.HTML, "answer-input") {
		t.Errorf("typed front = %q (type_in=%v), want the answer input markup", typed.Front.HTML, typed.TypeIn)
	}
	if !strings.Contains(typed.Back.HTML, "expected") {
		t.Errorf("typed back = %q, want the expected answer", typed.Back.HTML)
	}
	if !basic.AnswerIncludesQuestion || !strings.Contains(basic.Back.HTML, "Basic question") {
		t.Errorf("basic back = %q (includes question=%v), want the question inside the answer face", basic.Back.HTML, basic.AnswerIncludesQuestion)
	}
	for _, card := range page.Cards {
		if card.Front.Side != "front" || card.Back.Side != "back" {
			t.Errorf("card sides = %q/%q, want front/back", card.Front.Side, card.Back.Side)
		}
		if card.Front.BodyClass != "card" || card.Back.BodyClass != "card" {
			t.Errorf("body class = %q/%q, want card", card.Front.BodyClass, card.Back.BodyClass)
		}
		if card.Front.CSS == "" || card.Front.CSS != card.Back.CSS {
			t.Errorf("css = %q/%q, want one non-empty stored value", card.Front.CSS, card.Back.CSS)
		}
		if card.Front.MediaBase != card.Back.MediaBase {
			t.Errorf("media base = %q/%q, want one value per card", card.Front.MediaBase, card.Back.MediaBase)
		}
	}

	// Without the frame header the face keeps the SPA's form: the input marker is stripped, so the
	// control can never be driven outside the frame.
	plain := decodePreviewPage(t, previewGet(t, srv, base+"?sort=note_type", cookies, false))
	for _, card := range plain.Cards {
		if card.NoteType != "Typed answer" {
			continue
		}
		if strings.Contains(card.Front.HTML, "answer-input") {
			t.Errorf("non-frame front carries the answer input: %q", card.Front.HTML)
		}
		if !strings.Contains(card.Front.HTML, "Type the answer") {
			t.Errorf("non-frame front = %q, want the prompt text", card.Front.HTML)
		}
	}
}

// TestDeckPreviewCardsExcludesDeletedContent pins the visibility rule: a soft-deleted note's cards
// and a soft-deleted card are omitted, while the total counts only the visible ones.
func TestDeckPreviewCardsExcludesDeletedContent(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview deleted deck")
	basic := seedBasic(t, db, deck.ID, "Q", "A")
	cloze := seedCloze(t, db, deck.ID)
	base := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	if page := decodePreviewPage(t, previewGet(t, srv, base, cookies, false)); page.Total != 3 {
		t.Fatalf("total before deletion = %d, want 3", page.Total)
	}
	now := time.Now().UTC()
	if err := db.Model(&store.Note{}).Where("id = ?", basic.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("soft-delete note: %v", err)
	}
	if page := decodePreviewPage(t, previewGet(t, srv, base, cookies, false)); page.Total != 2 {
		t.Fatalf("total after note deletion = %d, want 2", page.Total)
	}
	cards, err := store.NewCardStore(db).ByNote(context.Background(), cloze.ID)
	if err != nil || len(cards) != 2 {
		t.Fatalf("load cloze cards: %v (%d cards)", err, len(cards))
	}
	if err := db.Model(&store.Card{}).Where("id = ?", cards[0].ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("soft-delete card: %v", err)
	}
	if page := decodePreviewPage(t, previewGet(t, srv, base, cookies, false)); page.Total != 1 {
		t.Fatalf("total after card deletion = %d, want 1", page.Total)
	}
}

// TestDeckPreviewCardsNoteTypeSections covers the grouping the note type jump needs: only the note
// type order carries it, each section's first position is the list position of its first card, and
// the counts add up to the deck total.
func TestDeckPreviewCardsNoteTypeSections(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview sections deck")
	seedBasic(t, db, deck.ID, "Q", "A")
	seedCloze(t, db, deck.ID)
	base := "/api/v1/decks/" + deck.PublicID + "/preview/cards"

	// A jump target is a position in the note type order, so the creation order carries no grouping.
	created := decodePreviewPage(t, previewGet(t, srv, base+"?sort=created", cookies, false))
	if len(created.NoteTypes) != 0 {
		t.Fatalf("creation order carries %d note type sections, want none", len(created.NoteTypes))
	}

	page := decodePreviewPage(t, previewGet(t, srv, base+"?sort=note_type&per_page=100", cookies, false))
	want := []previewNoteTypeSection{
		{Name: "Basic", Count: 1, FirstPosition: 1},
		{Name: "Cloze", Count: 2, FirstPosition: 2},
	}
	if !slices.Equal(page.NoteTypes, want) {
		t.Fatalf("note type sections = %+v, want %+v", page.NoteTypes, want)
	}
	var sum int64
	for _, section := range page.NoteTypes {
		sum += section.Count
		if section.FirstPosition < 1 || section.FirstPosition > int64(len(page.Cards)) {
			t.Errorf("section %q first position = %d, outside the %d listed cards",
				section.Name, section.FirstPosition, len(page.Cards))
			continue
		}
		if got := page.Cards[section.FirstPosition-1].NoteType; got != section.Name {
			t.Errorf("card at position %d is %q, want the section's %q", section.FirstPosition, got, section.Name)
		}
	}
	if sum != page.Total {
		t.Errorf("sections count %d cards, want the total %d", sum, page.Total)
	}
}

// previewNoteStepBody is the external shape of one note step answer.
type previewNoteStepBody struct {
	Position *int64 `json:"position"`
}

// decodeNoteStep decodes a successful note step response.
func decodeNoteStep(t *testing.T, rec *httptest.ResponseRecorder) *int64 {
	t.Helper()
	var body previewNoteStepBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode note step body: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body.Position
}

// TestDeckPreviewNoteStep covers the preview's note stepping: the cursor and direction resolve to
// the first card of the adjacent note in the requested order, and a deck edge answers null.
func TestDeckPreviewNoteStep(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Preview note step deck")
	// Creation order: A at 1, B at 2..5, C at 6.
	seedBasic(t, db, deck.ID, "Q1", "A1")
	seedClozeText(t, db, deck.ID, "{{c1::a}} {{c2::b}} {{c3::c}} {{c4::d}}")
	seedBasic(t, db, deck.ID, "Q2", "A2")
	base := "/api/v1/decks/" + deck.PublicID + "/preview/note-step"

	if rec := previewGet(t, srv, base+"?from=1&dir=next", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	strangerID, strangerCookies, _ := createUserAndLogin(t, srv, db, "preview-note-step-stranger")
	if rec := previewGet(t, srv, base+"?from=1&dir=next", strangerCookies, false); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger GET = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	grantRole(t, srv, deck.ID, strangerID, store.RoleReader, cookies, csrf)
	if rec := previewGet(t, srv, base+"?from=1&dir=next", strangerCookies, false); rec.Code != http.StatusOK {
		t.Fatalf("granted reader GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	cases := []struct {
		name    string
		query   string
		want    int64
		present bool
	}{
		{name: "forward into the four-card note", query: "?from=1&dir=next&sort=created", want: 2, present: true},
		{name: "forward past the four-card note", query: "?from=2&dir=next&sort=created", want: 6, present: true},
		{name: "forward from inside the note", query: "?from=5&dir=next&sort=created", want: 6, present: true},
		{name: "no note after the last", query: "?from=6&dir=next&sort=created"},
		{name: "back into the previous note at its first card", query: "?from=6&dir=prev&sort=created", want: 2, present: true},
		{name: "back from inside the note", query: "?from=3&dir=prev&sort=created", want: 1, present: true},
		{name: "no note before the first", query: "?from=1&dir=prev&sort=created"},
		{name: "cursor past the end", query: "?from=99&dir=next&sort=created"},
		{name: "note type order steps between notes of one type", query: "?from=1&dir=next&sort=note_type", want: 2, present: true},
		{name: "note type order into the next type", query: "?from=2&dir=next&sort=note_type", want: 3, present: true},
		{name: "note type order ends after the last type", query: "?from=3&dir=next&sort=note_type"},
		{name: "note type order backwards", query: "?from=3&dir=prev&sort=note_type", want: 2, present: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := previewGet(t, srv, base+tc.query, cookies, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200 (body %s)", tc.query, rec.Code, snippet(rec.Body.String()))
			}
			got := decodeNoteStep(t, rec)
			if !tc.present {
				if got != nil {
					t.Errorf("position = %d, want null", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("position = null, want %d", tc.want)
			}
			if *got != tc.want {
				t.Errorf("position = %d, want %d", *got, tc.want)
			}
		})
	}

	if rec := previewGet(t, srv, "/api/v1/decks/no-such-deck/preview/note-step?from=1&dir=next", cookies, false); rec.Code != http.StatusNotFound {
		t.Errorf("unknown deck GET = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	for _, query := range []string{
		"",
		"?dir=next",
		"?from=0&dir=next",
		"?from=abc&dir=next",
		"?from=1",
		"?from=1&dir=sideways",
		"?from=1&dir=next&sort=bogus",
	} {
		if rec := previewGet(t, srv, base+query, cookies, false); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400 (body %s)", query, rec.Code, snippet(rec.Body.String()))
		}
	}
}
