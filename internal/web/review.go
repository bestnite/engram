package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/notetype"
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

// CardFace is one rendered side of a card: the HTML to write into the frame, the note type's CSS, the body class,
// which side it is, the media base its relative media names resolve against, and the audio/TTS tags that HTML
// references as [anki:play:<side>:<n>] markers.
//
// This object is the card-view contract. The transitional front_html/back_html fields travel next to it until the frame
// runtime reads this object directly.
type CardFace struct {
	HTML      string `json:"html"`
	CSS       string `json:"css"`
	BodyClass string `json:"body_class"`
	Side      string `json:"side"`
	MediaBase string `json:"media_base"`
	// AVTags describes every play marker in HTML. The front face carries its question tags; the answer face carries
	// the question tags inserted by {{FrontSide}} plus the tags the answer itself adds, each with the side it was
	// extracted from, so the client can play the answer's own tags without replaying the question's.
	AVTags []notetype.AVTag `json:"av_tags"`
	// Autoplay is the deck's setting for this face: start its tags without a click.
	Autoplay bool `json:"autoplay"`
}

// ReviewCardView is the rendered two sides of the current card plus its identity.
type ReviewCardView struct {
	CardID string
	NoteID string
	DeckID string
	// ExpectedVersion is the optimistic-lock version carried on submit ("0" for a new card).
	ExpectedVersion string
	Template        string
	// Ordinal is the card's zero-based position among the note's templates, the value Anki's reviewer turns into the
	// body class `cardN` with N = Ordinal + 1. It is zero for a single-template note and for a cloze card.
	Ordinal int
	// Front and Back are the card faces: HTML, CSS, body class, side and media base.
	Front CardFace
	Back  CardFace
	// FrontHTML / BackHTML are the transitional flat fields the SPA reads until it switches to Front and Back.
	FrontHTML string
	BackHTML  string
	// OptionsHTML holds one HTML fragment per option of a choice card, in option order; nil for every other card kind.
	// The review page renders these fragments inside its own answer controls, so the answer control's own markup stays
	// out of the card face.
	OptionsHTML []string
	// OptionOrder is the display order of a choice card's options: display slot b shows stored option index OptionOrder[b].
	// Each OptionsHTML entry stays at its stored index, and the submitted answer stays a stored option index.
	OptionOrder []int
	// Blanks holds the hint for each input box of per-blank/per-item answer controls (cloze and list), in order, empty
	// string when there is no hint; the review page uses it to decide the number of input boxes.
	Blanks []string
	// EditHref points at the edit page of that note, for the e-key jump.
	EditHref string
	// MediaBase is the frame document's <base href> ("/fm/<token>/") for this user and this card's deck. It is set on every
	// card-face response so a frame consumer can resolve the relative media names; only a request carrying the
	// X-Engram-Frame: 1 header receives those relative names, and every other request keeps the absolute /media/ URLs.
	// Empty when the frame media feature is unavailable (secrets not wired).
	MediaBase string
}

// typeMarkerPattern matches the placeholder the type filter renders, in all of its written forms.
var typeMarkerPattern = regexp.MustCompile(`\[\[type:[^\]]*\]\]`)

// cardFace renders one card and resolves its note type, template and answer bindings.
type cardFace struct {
	card     *store.Card
	note     *store.Note
	deck     *store.Deck
	noteType *store.NoteType
	config   notetype.Config
	template notetype.Template
	spec     *notetype.AnswerSpec
	ordinal  int
	values   map[string]string
	// frontRaw and backRaw are the template output before audio/TTS extraction. They keep the literal [sound:] and
	// [anki:tts] tags for non-frame consumers.
	frontRaw string
	backRaw  string
	// frontHTML and backHTML replace every audio/TTS tag with its play marker; frontAV and backAV describe those
	// markers. The answer text was rendered with the already-extracted question in place of {{FrontSide}}, so backAV
	// holds the answer's own tags and the question's, while backHTML's question part still carries question markers.
	frontHTML string
	backHTML  string
	frontAV   []notetype.AVTag
	backAV    []notetype.AVTag
}

// renderCard loads the card's note, note type and template and renders both sides with the card's current data.
//
// The front is rendered without an answer and the back with the given answer result (nil before a submission), so the back
// can show the answer control's breakdown of what the learner wrote. The raw template output still carries the
// [[type:...]] marker; callers decide how to replace or strip it.
func (s *Server) renderCard(ctx context.Context, card *store.Card, submitted json.RawMessage, result *answer.GradeResult) (*cardFace, error) {
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return nil, err
	}
	nt, cfg, err := s.notes.NoteTypeFor(ctx, note)
	if err != nil {
		return nil, err
	}
	tmpl, clozeOrdinal, err := notetype.CardTemplate(cfg, card.Template)
	if err != nil {
		return nil, err
	}
	deck, err := s.decks.ByID(ctx, note.DeckID)
	if err != nil {
		return nil, err
	}
	values := store.NoteFieldValues(note.FieldsJSON)
	deckName, subdeckName := deckFieldNames(deck.Name, card.DeckPath)
	data := notetype.RenderData{
		Fields:       values,
		Tags:         store.TagsOrEmpty(note.TagsJSON),
		TypeName:     nt.Name,
		DeckName:     deckName,
		SubdeckName:  subdeckName,
		CardName:     tmpl.Name,
		CardID:       card.PublicID,
		ClozeOrdinal: clozeOrdinal,
	}
	frontRaw, err := notetype.Render(cfg, tmpl, notetype.SideFront, data)
	if err != nil {
		return nil, err
	}
	frontHTML, frontAV := notetype.ExtractAVTags(frontRaw, "q")
	// The raw back keeps the question's literal tags for non-frame consumers; the extracted back uses the extracted
	// question for {{FrontSide}}, which is what keeps the question's audio out of the answer's own tag list.
	backRaw, err := notetype.RenderAnswer(cfg, tmpl, data, frontRaw)
	if err != nil {
		return nil, err
	}
	answeredRaw, err := notetype.RenderAnswer(cfg, tmpl, data, frontHTML)
	if err != nil {
		return nil, err
	}
	backHTML, answerAV := notetype.ExtractAVTags(answeredRaw, "a")
	// The question's tags belong to the answer face only when {{FrontSide}} actually inserted their markers; a template
	// that answers without FrontSide then carries no unreferenced q tag.
	backAV := answerAV
	if strings.Contains(answeredRaw, "[anki:play:q:") {
		backAV = append(append([]notetype.AVTag{}, frontAV...), answerAV...)
	}
	return &cardFace{
		card: card, note: note, deck: deck, noteType: nt, config: cfg, template: tmpl,
		spec: tmpl.Answer, ordinal: clozeOrdinal, values: values,
		frontRaw: frontRaw, backRaw: backRaw, frontHTML: frontHTML, backHTML: backHTML,
		frontAV: frontAV, backAV: backAV,
	}, nil
}

// deckFieldNames returns the {{Deck}} and {{Subdeck}} values for a card: the deck name with the card's stored subdeck
// path appended, and the last "::"-separated component of that joined name.
//
// The card's deck_path carries the Anki subdeck path below the deck the card was imported into, so {{Deck}} shows the
// same chain the imported card shows and {{Subdeck}} shows the innermost subdeck. An empty deck_path is treated as no
// path, because the column is nullable and an empty string carries no path component to append.
func deckFieldNames(deckName string, deckPath *string) (deck, subdeck string) {
	deck = deckName
	if deckPath != nil && *deckPath != "" {
		deck = deckName + "::" + *deckPath
	}
	subdeck = deck
	if i := strings.LastIndex(deck, "::"); i >= 0 {
		subdeck = deck[i+2:]
	}
	return deck, subdeck
}

// faces renders both sides into the card-view shape.
//
// For a frame request the front side substitutes the answer control's input markup for the type marker, so the frame
// draws the control and posts its value. A non-frame request keeps the marker stripped: the transitional SPA draws its
// own controls from options_html and blanks. The back side substitutes the control's breakdown for the marker, so the
// correct answer is visible with or without a submitted answer.
func (s *Server) faces(ctx context.Context, user *store.User, item *cardFace, submitted json.RawMessage, result *answer.GradeResult, forFrame bool, optionOrder []int) (CardFace, CardFace) {
	mediaBase := s.frameMediaBase(ctx, user.ID, item.note.DeckID)
	frontHTML, backHTML := item.frontRaw, item.backRaw
	frontAV, backAV := []notetype.AVTag{}, []notetype.AVTag{}
	if forFrame {
		frontHTML, backHTML = item.frontHTML, item.backHTML
		frontAV, backAV = item.frontAV, item.backAV
	}
	frontHTML = frontFaceHTML(item, frontHTML, optionOrder, forFrame)
	backHTML = backFaceHTML(item, backHTML, submitted, result, optionOrder)
	if !forFrame || mediaBase == "" {
		frontHTML = absoluteMediaRefs(frontHTML)
		backHTML = absoluteMediaRefs(backHTML)
	}
	bodyClass := "card"
	autoplay := item.deck != nil && item.deck.AutoplayEnabled()
	return CardFace{
		HTML:      frontHTML,
		CSS:       item.config.CSS,
		BodyClass: bodyClass,
		Side:      notetype.SideFront.String(),
		MediaBase: mediaBase,
		AVTags:    frontAV,
		Autoplay:  autoplay,
	}, CardFace{
		HTML:      backHTML,
		CSS:       item.config.CSS,
		BodyClass: bodyClass,
		Side:      notetype.SideBack.String(),
		MediaBase: mediaBase,
		AVTags:    backAV,
		Autoplay:  autoplay,
	}
}

// frontFaceHTML renders the front side. A frame request replaces the type marker with the answer control's input
// markup; every other request strips the marker, because the transitional SPA draws its own control.
func frontFaceHTML(item *cardFace, source string, optionOrder []int, forFrame bool) string {
	marker := typeMarkerPattern.FindString(source)
	if !forFrame || item.spec == nil || marker == "" {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	control, ok := answer.Lookup(item.spec.Control)
	if !ok {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	bindings, err := answer.ResolveBindings(*item.spec, item.values)
	if err != nil {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	rendered, err := answer.RenderSide(control, answer.RenderContext{
		Marker:       marker,
		Side:         notetype.SideFront,
		Bindings:     bindings,
		ClozeOrdinal: item.ordinal,
		OptionOrder:  optionOrder,
	}, source)
	if err != nil {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	return rendered
}

// backFaceHTML replaces the back side's type marker with the answer control's breakdown when a control exists, and
// strips the marker otherwise.
func backFaceHTML(item *cardFace, source string, submitted json.RawMessage, result *answer.GradeResult, optionOrder []int) string {
	marker := typeMarkerPattern.FindString(source)
	if item.spec == nil || marker == "" {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	control, ok := answer.Lookup(item.spec.Control)
	if !ok {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	bindings, err := answer.ResolveBindings(*item.spec, item.values)
	if err != nil {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	rendered, err := answer.RenderSide(control, answer.RenderContext{
		Marker:       marker,
		Side:         notetype.SideBack,
		Bindings:     bindings,
		ClozeOrdinal: item.ordinal,
		OptionOrder:  optionOrder,
		Answer:       submitted,
		Result:       result,
	}, source)
	if err != nil {
		return typeMarkerPattern.ReplaceAllString(source, "")
	}
	return rendered
}

// optionLines splits a stored options field into its entries.
//
// The entries are HTML fragments, not answer text, so they are split at the line separator only and kept verbatim: running
// them through PlainText would strip the markup an option is allowed to carry.
func optionLines(html string) []string {
	segments := optionLineBreak.Split(html, -1)
	out := make([]string, 0, len(segments))
	for _, segment := range segments {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// optionLineBreak matches the separator the write path puts between array entries.
var optionLineBreak = regexp.MustCompile(`(?i)<br\s*/?>|\r?\n`)

// answerOptionCount returns the number of options an answer control reads, or zero when it reads none.
func answerOptionCount(spec *notetype.AnswerSpec, values map[string]string) int {
	control, ok := answer.Lookup(spec.Control)
	if !ok {
		return 0
	}
	roles, ok := control.(answer.Roles)
	if !ok {
		return 0
	}
	for _, role := range roles.Roles() {
		if role.Name != "options" {
			continue
		}
		return len(optionLines(values[spec.Bindings[role.Name]]))
	}
	return 0
}

// optionHTML returns one HTML fragment per option of a choice answer control, in stored order.
func optionHTML(spec *notetype.AnswerSpec, values map[string]string) []string {
	if spec == nil {
		return nil
	}
	control, ok := answer.Lookup(spec.Control)
	if !ok {
		return nil
	}
	roles, ok := control.(answer.Roles)
	if !ok {
		return nil
	}
	for _, role := range roles.Roles() {
		if role.Name != "options" {
			continue
		}
		lines := optionLines(values[spec.Bindings[role.Name]])
		if len(lines) == 0 {
			return nil
		}
		return lines
	}
	return nil
}

// blankHints returns one hint per input box the answer control draws for this card.
func blankHints(spec *notetype.AnswerSpec, values map[string]string, clozeOrdinal int) []string {
	if spec == nil {
		return nil
	}
	switch spec.Control {
	case "cloze_blanks":
		return notetype.ExtractClozeHints(values[spec.Bindings["text"]], clozeOrdinal)
	case "list":
		return make([]string, len(answer.ParseLines(values[spec.Bindings["items"]])))
	default:
		return nil
	}
}

// cardView renders both sides of a card and fetches the optimistic-lock version.
func (s *Server) cardView(ctx context.Context, user *store.User, item schedule.QueueItem) (*ReviewCardView, error) {
	return s.cardViewWithAnswer(ctx, user, item, nil, nil, false)
}

// cardViewWithAnswer renders both sides and, when an answer result is given, renders the back with that answer.
func (s *Server) cardViewWithAnswer(ctx context.Context, user *store.User, item schedule.QueueItem, submitted json.RawMessage, result *answer.GradeResult, forFrame bool) (*ReviewCardView, error) {
	card, err := s.cards.ByID(ctx, item.CardID)
	if err != nil {
		return nil, err
	}
	rendered, err := s.renderCard(ctx, card, submitted, result)
	if err != nil {
		return nil, err
	}
	version := s.stateVersion(ctx, user.ID, card.ID)
	// The choice options stay in the parent page, which has no <base> element, so their media references are always absolute.
	options := optionHTML(rendered.spec, rendered.values)
	for i := range options {
		options[i] = absoluteMediaRefs(options[i])
	}
	var optionOrder []int
	if len(options) > 1 {
		optionOrder = api.OptionOrder(len(options), user.ID, card.ID, version)
	}
	front, back := s.faces(ctx, user, rendered, submitted, result, forFrame, optionOrder)
	return &ReviewCardView{
		CardID:          card.PublicID,
		NoteID:          rendered.note.PublicID,
		DeckID:          rendered.deck.PublicID,
		ExpectedVersion: strconv.Itoa(version),
		Template:        card.Template,
		Ordinal:         card.Ordinal,
		Front:           front,
		Back:            back,
		FrontHTML:       front.HTML,
		BackHTML:        back.HTML,
		OptionsHTML:     options,
		OptionOrder:     optionOrder,
		Blanks:          blankHints(rendered.spec, rendered.values, rendered.ordinal),
		EditHref:        "/decks/" + rendered.deck.PublicID + "/notes/" + rendered.note.PublicID,
		MediaBase:       front.MediaBase,
	}, nil
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

// cardIsMachineGraded reports whether the card's template grades a submitted answer.
func (s *Server) cardIsMachineGraded(ctx context.Context, card *store.Card) (bool, error) {
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return false, err
	}
	_, cfg, err := s.notes.NoteTypeFor(ctx, note)
	if err != nil {
		return false, err
	}
	tmpl, _, err := notetype.CardTemplate(cfg, card.Template)
	if err != nil {
		return false, err
	}
	return answer.IsMachineGraded(tmpl.Answer), nil
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
	case int64:
		return strconv.FormatInt(n, 10)
	case json.Number:
		return n.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
