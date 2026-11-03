package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// Deck content preview: a reader pages through every card of one deck and sees both rendered
// faces without studying. The page is the app shell; the data comes from one session JSON
// endpoint that renders a page of cards with the same renderer the review page uses, so the
// preview shows exactly what review would show (answer controls, audio, TTS, image occlusion).
// A second, lightweight endpoint answers where a step to the adjacent note lands, so one click
// skips the remaining cards of a multi-card note.
//
// No progress is written and no card state is read: the endpoints are reads of the deck's
// content only, which is why the reader role is enough and why suspension does not filter the
// list.

// registerPreviewRoutes mounts the preview page and its JSON endpoints.
//
// Skipped when a dependency is not wired, so M0-stage tests can construct a Server without them.
func (s *Server) registerPreviewRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.cards == nil || s.notes == nil {
		return
	}
	router.GET("/decks/:id/preview", s.deckPreviewPageRoute)
	router.GET("/api/v1/decks/:id/preview/cards", s.deckPreviewCards)
	router.GET("/api/v1/decks/:id/preview/note-step", s.deckPreviewNoteStep)
}

// deckPreviewPageRoute serves GET /decks/:id/preview: the page needs a signed-in session and at
// least the reader role on the deck before the shell is handed out, so an unauthorized user
// never receives the preview page.
func (s *Server) deckPreviewPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// previewCardFace is one rendered card of a preview page: both faces plus the identity and the
// flags the client needs to frame and label the card. The faces are the review renderer's own
// type, so a change to the review face reaches the preview without a second mapping.
type previewCardFace struct {
	CardID                 string   `json:"card_id"`
	NoteID                 string   `json:"note_id"`
	NoteType               string   `json:"note_type"`
	Template               string   `json:"template"`
	Ordinal                int      `json:"ordinal"`
	Front                  CardFace `json:"front"`
	Back                   CardFace `json:"back"`
	TypeIn                 bool     `json:"type_in"`
	AnswerIncludesQuestion bool     `json:"answer_includes_question"`
	// Empty reports a card whose front no longer renders anything, such as the reverse card of a
	// note whose reverse field was cleared. The card stays in the list until empty-card cleanup,
	// so the preview shows the same notice review shows instead of a blank frame.
	Empty    bool   `json:"empty"`
	EditHref string `json:"edit_href"`
}

// previewNoteTypeSection is one note type's block in the note type order: its name, how many
// cards it holds, and the 1-based position of its first card. The client jumps the cursor to
// FirstPosition, so the sections are the jump targets of the type selector.
type previewNoteTypeSection struct {
	Name          string `json:"name"`
	Count         int64  `json:"count"`
	FirstPosition int64  `json:"first_position"`
}

// previewCardsResponse is one page of a deck's cards, ordered as the request selected.
type previewCardsResponse struct {
	Cards   []previewCardFace `json:"cards"`
	Total   int64             `json:"total"`
	Page    int               `json:"page"`
	PerPage int               `json:"per_page"`
	Sort    string            `json:"sort"`
	// NoteTypes is the deck's note type grouping, present only for the note type order so the
	// page can offer a jump to a type. The counts are visible cards, so they add up to Total.
	NoteTypes []previewNoteTypeSection `json:"note_types,omitempty"`
}

// previewNoteTypeSections turns per-type card counts into jump targets. Sections are ordered by
// name, which is the list order, so each first position is the running sum of the earlier counts.
func previewNoteTypeSections(counts []store.CardNoteTypeCount) []previewNoteTypeSection {
	sections := make([]previewNoteTypeSection, 0, len(counts))
	position := int64(1)
	for _, count := range counts {
		sections = append(sections, previewNoteTypeSection{
			Name:          count.Name,
			Count:         count.Count,
			FirstPosition: position,
		})
		position += count.Count
	}
	return sections
}

// deckPreviewCards serves GET /api/v1/decks/:id/preview/cards.
//
// It requires the reader role, like the page. The response is frame-ready HTML: a browser page
// renders a card face only inside the sandboxed frame, and the frame needs the media base the
// renderer puts on each face, so the client asks for frame output with the frame header.
func (s *Server) deckPreviewCards(c *gin.Context) {
	deck, user, ok := s.previewDeck(c)
	if !ok {
		return
	}
	opts, ok := previewCardListOptions(c)
	if !ok {
		return
	}
	cards, total, err := s.cards.ListByDeck(c.Request.Context(), deck.ID, opts)
	if err != nil {
		s.logger.Error("list deck preview cards failed", "deck_id", deck.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	forFrame := frameOutputRequested(c)
	views := make([]previewCardFace, 0, len(cards))
	for i := range cards {
		card := &cards[i]
		rendered, err := s.renderCard(c.Request.Context(), card, nil, nil)
		if err != nil {
			s.logger.Error("render deck preview card failed", "card_id", card.ID, "error", err)
			writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
			return
		}
		// A browse answer is not graded, so the choice display order stays the stored order: the
		// reviewer shuffles by the viewer's state version, and a preview should read the same for
		// every reader of the deck.
		front, back := s.faces(c.Request.Context(), user, rendered, nil, nil, forFrame, nil)
		if rendered.empty {
			front.HTML = emptyCardNoticeHTML(c.Request.Context())
			front.AVTags = nil
		}
		views = append(views, previewCardFace{
			CardID:                 card.PublicID,
			NoteID:                 rendered.note.PublicID,
			NoteType:               rendered.noteType.Name,
			Template:               card.Template,
			Ordinal:                card.Ordinal,
			Front:                  front,
			Back:                   back,
			TypeIn:                 rendered.spec == nil && typeMarkerPattern.MatchString(rendered.frontRaw),
			AnswerIncludesQuestion: notetype.AnswerIncludesQuestion(rendered.config, rendered.template),
			Empty:                  rendered.empty,
			EditHref:               "/decks/" + deck.PublicID + "/notes/" + rendered.note.PublicID,
		})
	}
	response := previewCardsResponse{
		Cards:   views,
		Total:   total,
		Page:    opts.Page,
		PerPage: opts.PerPage,
		Sort:    opts.Sort,
	}
	// The grouping costs one count query and only the note type order uses it: a jump target is a
	// position in that order, so the creation order has nothing to jump to.
	if opts.Sort == store.CardListSortNoteType {
		counts, err := s.cards.CountByNoteType(c.Request.Context(), deck.ID)
		if err != nil {
			s.logger.Error("count deck preview note types failed", "deck_id", deck.ID, "error", err)
			writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
			return
		}
		response.NoteTypes = previewNoteTypeSections(counts)
	}
	c.JSON(http.StatusOK, response)
}

// previewDeck resolves the deck of a preview JSON request and checks the reader role. It writes
// the error response and reports false when the caller may not read the deck.
func (s *Server) previewDeck(c *gin.Context) (*store.Deck, *store.User, bool) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return nil, nil, false
	}
	deck, err := s.decks.ByPublicID(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, false
	}
	if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleReader); !ok {
		return nil, nil, false
	}
	return deck, user, true
}

// previewCardListOptions parses the page, per_page and sort query parameters.
//
// A parameter that is present but malformed is a request error rather than a silent fallback:
// a client that sends page=abc expects page 1 and would page through the deck forever without
// noticing. The paging bounds themselves are the store's NormalizeCardListOptions.
func previewCardListOptions(c *gin.Context) (store.CardListOptions, bool) {
	opts := store.CardListOptions{Page: 1, Sort: store.CardListSortCreated}
	if raw, present := c.GetQuery("page"); present {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 1 {
			writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return opts, false
		}
		opts.Page = value
	}
	if raw, present := c.GetQuery("per_page"); present {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || value < 1 {
			writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return opts, false
		}
		opts.PerPage = value
	}
	sort, ok := previewSort(c)
	if !ok {
		return opts, false
	}
	opts.Sort = sort
	return store.NormalizeCardListOptions(opts), true
}

// previewSort reads the sort query parameter: absent means the creation order, and a value outside
// the two orders is a request error rather than a silent fallback, because the client's cursor and
// a note step are positions in exactly one order.
func previewSort(c *gin.Context) (string, bool) {
	raw, present := c.GetQuery("sort")
	if !present {
		return store.CardListSortCreated, true
	}
	switch strings.TrimSpace(raw) {
	case store.CardListSortCreated:
		return store.CardListSortCreated, true
	case store.CardListSortNoteType:
		return store.CardListSortNoteType, true
	}
	writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
	return "", false
}

// previewNoteStepResponse is the jump target of one note step: the 1-based position of the first
// card of the adjacent note, or null when the deck has no other note in that direction.
type previewNoteStepResponse struct {
	Position *int64 `json:"position"`
}

// deckPreviewNoteStep serves GET /api/v1/decks/:id/preview/note-step.
//
// The client asks where a note step lands instead of finding the boundary itself: a note's cards
// can cross a page and, after a template is added to a note type, need not be adjacent in the list,
// so only the server can resolve the neighbouring note in the same order the card list uses without
// fetching and rendering pages the reader never sees. It requires the reader role and writes
// nothing.
func (s *Server) deckPreviewNoteStep(c *gin.Context) {
	deck, _, ok := s.previewDeck(c)
	if !ok {
		return
	}
	from, forward, ok := previewNoteStepCursor(c)
	if !ok {
		return
	}
	sort, ok := previewSort(c)
	if !ok {
		return
	}
	position, err := s.cards.NoteStepPosition(c.Request.Context(), deck.ID, sort, from, forward)
	if err != nil {
		s.logger.Error("resolve deck preview note step failed",
			"deck_id", deck.ID, "from", from, "forward", forward, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	response := previewNoteStepResponse{}
	if position > 0 {
		response.Position = &position
	}
	c.JSON(http.StatusOK, response)
}

// previewNoteStepCursor parses the 1-based cursor position and the direction of one note step.
func previewNoteStepCursor(c *gin.Context) (int, bool, bool) {
	raw, present := c.GetQuery("from")
	if !present {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return 0, false, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return 0, false, false
	}
	switch strings.TrimSpace(c.Query("dir")) {
	case "next":
		return value, true, true
	case "prev":
		return value, false, true
	}
	writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
	return 0, false, false
}
