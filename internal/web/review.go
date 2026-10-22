package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// Canonical path of the review page. Grading and actions go through the SPA's same-origin JSON endpoints (/api/v1/review/*).
const reviewPagePath = "/review"

// registerReviewRoutes mounts the review page.
// Skips when the dependencies are not wired, so M0-stage tests can still construct a Server.
func (s *Server) registerReviewRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.cards == nil || s.presets == nil {
		return
	}
	// GET /review returns the app shell: reviewPageRoute first applies the usual session and deck-scope authorization checks, then hands out the app shell,
	// and the client router renders the review page. Grading and actions go through the SPA's JSON endpoints
	// (PATCH/POST /api/v1/review/*); there is no separate htmx grading endpoint any more.
	router.GET(reviewPagePath, s.reviewPageRoute)
}

// reviewPageRoute serves GET /review: it returns the app shell, and the client router renders the review page,
// while data still goes through the existing JSON endpoints. Session and deck-scope decisions run before switching to the shell —
// an illegal deck parameter is 400, an unreadable deck in the scope is 403/404, and neither is let through just because the app shell is returned.
func (s *Server) reviewPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if _, ok := s.parseDeckScope(c, user); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// reviewScope is the scope of one review request: an empty deckIDs means the whole library (no deck filtering); when tags is non-empty only cards
// carrying any one of those tags are taken (in that case deckIDs is exactly one, validated by api.ReviewTags).
// The scope is decided by the repeatable deck / tag URL parameters and carried back verbatim by the SPA's JSON requests.
type reviewScope struct {
	deckIDs []uint64
	tags    []string
}

// deckScopeFromValues parses the repeatable deck parameter values (public id strings): dedupes, skips empty strings; any value with
// an illegal shape is 400, a legal shape that does not exist in the store is 404. It then validates each one with loadDeckForRole(..., RoleReader):
// any missing or unauthorized deck fails the whole request (404/403, written by that helper), never silently dropping a deck.
// rawTags is normalized and validated via api.ReviewTags: tags with anything but exactly one deck is 400.
func (s *Server) deckScopeFromValues(c *gin.Context, user *store.User, raw, rawTags []string) (reviewScope, bool) {
	ids := make([]uint64, 0, len(raw))
	seen := make(map[uint64]bool, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !looksLikePublicID(v) {
			c.AbortWithStatus(http.StatusBadRequest)
			return reviewScope{}, false
		}
		deck, err := s.decks.ByPublicID(c.Request.Context(), v)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return reviewScope{}, false
		}
		if seen[deck.ID] {
			continue
		}
		seen[deck.ID] = true
		ids = append(ids, deck.ID)
	}
	for _, id := range ids {
		if _, ok := s.loadDeckForRole(c, user, id, store.RoleReader); !ok {
			return reviewScope{}, false
		}
	}
	tags, err := api.ReviewTags(ids, rawTags)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return reviewScope{}, false
	}
	return reviewScope{deckIDs: ids, tags: tags}, true
}

// parseDeckScope parses the repeatable deck / tag parameters in the GET query string (full-page review requests).
func (s *Server) parseDeckScope(c *gin.Context, user *store.User) (reviewScope, bool) {
	return s.deckScopeFromValues(c, user, c.QueryArray("deck"), c.QueryArray("tag"))
}

// ReviewCardView is the rendered two sides of the current card plus its identity.
// FrontHTML / BackHTML always come from internal/render's allowlist sanitizing, so the SPA can embed them as-is safely.
type ReviewCardView struct {
	CardID string
	NoteID string
	DeckID string
	// ExpectedVersion is the optimistic-lock version carried on submit ("0" for a new card).
	ExpectedVersion string
	Template        string
	FrontHTML       string
	BackHTML        string
	// Blanks holds the hint for each input box of per-blank/per-item answer types (cloze questions, list questions), in order, empty string when there is no hint;
	// the review page uses it to decide the number of input boxes. nil for other question types.
	Blanks []string
	// EditHref points at the edit page of that note, for the e-key jump.
	EditHref string
}

// cardView renders both sides of a card and fetches the optimistic-lock version.
func (s *Server) cardView(ctx context.Context, user *store.User, item schedule.QueueItem) (*ReviewCardView, error) {
	card, err := s.cards.ByID(ctx, item.CardID)
	if err != nil {
		return nil, err
	}
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return nil, err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return nil, cardtype.UnknownKindError(note.Kind)
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return nil, err
	}
	ct := cardtype.Card{Template: card.Template, Ordinal: card.Ordinal, Fields: fields}
	front, err := t.Render(ct, cardtype.SideFront)
	if err != nil {
		return nil, err
	}
	// For question types that declare OptionsField (choice questions) the review page renders the options as answer controls; if the same set of
	// options is also concatenated into FrontHTML, the page shows them twice — once as plain text, once as buttons. The preview and share pages have no
	// answer controls and still show Extra via renderSide, so they are only dropped here in the review view.
	if t.Describe().OptionsField != "" {
		front.Extra = nil
	}
	back, err := t.Render(ct, cardtype.SideBack)
	if err != nil {
		return nil, err
	}
	frontHTML, err := renderSide(front)
	if err != nil {
		return nil, err
	}
	backHTML, err := renderSide(back)
	if err != nil {
		return nil, err
	}
	var blanks []string
	if b, ok := t.(blankHinter); ok {
		blanks = b.BlankHints(ct)
	}
	deckPublic := s.deckPublicID(ctx, note.DeckID)
	return &ReviewCardView{
		CardID:          card.PublicID,
		NoteID:          note.PublicID,
		DeckID:          deckPublic,
		ExpectedVersion: strconv.Itoa(s.stateVersion(ctx, user.ID, card.ID)),
		Template:        card.Template,
		FrontHTML:       frontHTML,
		BackHTML:        backHTML,
		Blanks:          blanks,
		EditHref:        "/decks/" + deckPublic + "/notes/" + note.PublicID,
	}, nil
}

// blankHinter is the optional capability of per-blank/per-item answer types: give the hint for each input box of a card.
// The question type knows how many input boxes a card needs; the review page does not re-parse the fields.
type blankHinter interface {
	BlankHints(card cardtype.Card) []string
}

// stateVersion reads the optimistic-lock version for (card_id, user_id); a new card with no state row returns 0.
func (s *Server) stateVersion(ctx context.Context, userID, cardID uint64) int {
	var st store.CardState
	err := s.db.WithContext(ctx).Where("card_id = ? AND user_id = ?", cardID, userID).Take(&st).Error
	if err != nil {
		return 0
	}
	return st.Version
}

// graderFor decides whether a question type supports machine grading by asserting an optional narrow interface.
// Grading is an optional capability and the core pipeline does not depend on it: a type that does not implement Grader returns false and still goes through the four-grade self-assessment.
func graderFor(kind string) (cardtype.Grader, bool) {
	t, ok := cardtype.Lookup(kind)
	if !ok {
		return nil, false
	}
	g, ok := t.(cardtype.Grader)
	return g, ok
}

// numberText prints a number (a parsed answer) in its simplest form, so 50 does not show up as 50.000000.
func numberText(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(n), 'g', -1, 64)
	case int:
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}
