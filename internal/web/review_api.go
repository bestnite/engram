package web

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

type reviewRequest struct {
	CardID          string   `json:"card_id"`
	Rating          int      `json:"rating"`
	ExpectedVersion int      `json:"expected_version"`
	ElapsedMS       *int     `json:"elapsed_ms"`
	Deck            []string `json:"deck"`
	Tags            []string `json:"tags"`
	// Answer is the answer written down before self-assessment (short answer), archived with the self-assessment; other self-assessed types do not carry it.
	Answer json.RawMessage `json:"answer"`
}

// gradeRequest is the request body of the SPA grading entry point. For answer types the grade is produced by the server-side grader,
// and the client only submits the raw answer (Answer), never a grade — otherwise self-assessment would be masquerading as machine grading.
//
// Action decides the semantics of this request:
//   - "": normal grading, writes reviews (grade_source=typed);
//   - "reveal": only returns the card's back face with the answer breakdown, no grading, no DB write (read-only preview);
//   - "give_up": give up after the answer was revealed, logging one self-assessment entry as Again (grade_source=self).
type gradeRequest struct {
	CardID          string          `json:"card_id"`
	ExpectedVersion int             `json:"expected_version"`
	ElapsedMS       *int            `json:"elapsed_ms"`
	Deck            []string        `json:"deck"`
	Tags            []string        `json:"tags"`
	Action          string          `json:"action"`
	Answer          json.RawMessage `json:"answer"`
}

// reviewCardRequest is the request body of the SPA review page's two read-only/single-action entry points: bury and card render.
// Both carry only the target card and the review scope (decks and tags); the scope is carried back verbatim and the server rebuilds the queue from it.
type reviewCardRequest struct {
	CardID string   `json:"card_id"`
	Deck   []string `json:"deck"`
	Tags   []string `json:"tags"`
}

// reviewRenderRequest is the request body of the card render endpoint. Answer is the typed value a type-in card's frame
// posted; it only renders the answer side's comparison and is never written as progress.
type reviewRenderRequest struct {
	CardID string          `json:"card_id"`
	Deck   []string        `json:"deck"`
	Tags   []string        `json:"tags"`
	Answer json.RawMessage `json:"answer"`
}

// reviewAnswer gives the SPA a session and CSRF protected answer entry point; the business submit and the queue still reuse the API service.
func (s *Server) reviewAnswer(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, scope, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	// A self-assessment score for an answer type is rejected by the service (grading_required): for these cards the SPA goes to /api/v1/review/grade.
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: req.Rating, Answer: req.Answer, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeReviewResult(c, user, scope, result)
}

// reviewGrade is the SPA grading entry point for answer types: session + CSRF protected, the server uses the question type's grader
// to compute the grade and writes reviews per the preset's score→grade mapping (grade_source=typed, grade_detail_json).
// The client submits the raw answer, not a grade; grading rules are shared with SSR via graderFor / buildGradeInput.
func (s *Server) reviewGrade(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req gradeRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, note, scope, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	graded, err := s.cardIsMachineGraded(c.Request.Context(), card)
	if err != nil {
		s.logger.Error("resolve card template failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	if !graded {
		// A self-assessed template on the wrong entry point: rejected, so a four-grade score cannot pass itself off as machine grading.
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	switch req.Action {
	case gradeActionReveal:
		s.gradeReveal(c, user, card)
		return
	case gradeActionGiveUp:
		s.gradeGiveUp(c, user, req, scope)
		return
	case "":
	default:
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	s.gradeSubmit(c, user, card, note, req, scope)
}

// gradeSubmit hands the answer to the service for grading and writing, then assembles the grading result into page feedback.
// The grading rules (answer decoding, grading, score→grade mapping) all live in the service and the question types; this only does transport.
func (s *Server) gradeSubmit(c *gin.Context, user *store.User, card *store.Card, note *store.Note, req gradeRequest, scope reviewScope) {
	ctx := c.Request.Context()
	result, err := s.api.SubmitReview(ctx, user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Answer: req.Answer, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	feedback, err := s.gradeFeedback(ctx, user, card, req.Answer, result.Grade, frameOutputRequested(c))
	if err != nil {
		s.logger.Error("build spa grade feedback failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	s.writeReviewResult(c, user, scope, result, gin.H{"feedback": feedback})
}

// gradeReveal returns the card's back face with the answer breakdown, no grading and no DB write (revealing is a read-only preview).
// After revealing, the frontend switches to the "give up answering" state; only when the user clicks the button again does give_up log Again.
func (s *Server) gradeReveal(c *gin.Context, user *store.User, card *store.Card) {
	view, err := s.cardFaceView(c.Request.Context(), user, card, nil, nil, frameOutputRequested(c))
	if err != nil {
		s.logger.Error("render spa graded answer failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"revealed":    true,
		"card_id":     card.PublicID,
		"answer_html": view.Back.HTML,
		"back":        view.Back,
	})
}

// gradeGiveUp handles "answer already revealed, score 0 and continue": no grading, logs one self-assessment entry as Again.
// grade_source is recorded as self — this grade comes from the user giving up; no machine grading happened.
func (s *Server) gradeGiveUp(c *gin.Context, user *store.User, req gradeRequest, scope reviewScope) {
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, GiveUp: true, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeReviewResult(c, user, scope, result, gin.H{"gave_up": true})
}

// reviewBury is the SPA entry point for bury: session + CSRF protected. The business lives in the service's BuryCard (shared
// with REST and MCP: the caller's own card is postponed to their next review day, reader suffices); the response carries
// the same-scope queue counts and the next card rendered inline.
func (s *Server) reviewBury(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, scope, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	if _, err := s.api.BuryCard(c.Request.Context(), user, req.CardID); err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeQueue(c, user, scope, queueHead)
}

// reviewSuspend is the SPA entry point for the review page's "suspend this card": session + CSRF protected, the business lives in the service's
// SetCardSuspended (writes only the caller's own progress, reader suffices); the response carries the same-scope queue
// counts and the next card rendered inline.
func (s *Server) reviewSuspend(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, scope, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	if _, err := s.api.SetCardSuspended(c.Request.Context(), user, nil, req.CardID, true); err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeQueue(c, user, scope, queueHead)
}

// reviewUndoRequest is the request body of the undo entry point: besides the target card and deck scope, it must carry the
// state version the caller read (usually the version in the response right after a grade was submitted); undo binds to the target grade through it.
type reviewUndoRequest struct {
	CardID          string   `json:"card_id"`
	Deck            []string `json:"deck"`
	Tags            []string `json:"tags"`
	ExpectedVersion int      `json:"expected_version"`
}

// reviewUndo is the SPA entry point for "undo the grade just submitted": session + CSRF protected, the business lives in the service's
// UndoReview (which calls schedule.Rollback internally). Authorization uses reader — undo writes the caller's own
// (card_id, user_id) progress, so a reader of a shared deck can undo a grade they just made; any deck in the deck scope
// that is unreadable fails the whole request (reviewCard is reused, same criterion as bury).
//
// The request must carry expected_version: undo only takes effect when the version matches, and a replayed request or a stale-version request from a window opened twice
// returns 409 without deleting any grade. Response contract: the same-scope queue counts (queue_size / new_remaining), the undone card's public id
// (undone_card_id), and cards holding exactly that undone card. The queue is ordered by due_at, so the undone card is not guaranteed to be first;
// cards therefore carries the undone card itself rather than the queue head, and the frontend locates the current card by undone_card_id.
func (s *Server) reviewUndo(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewUndoRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, scope, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	result, err := s.api.UndoReview(c.Request.Context(), user, api.UndoReviewInput{
		CardID:          req.CardID,
		ExpectedVersion: req.ExpectedVersion,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeQueue(c, user, scope, s.undoneCard(user, result.CardID), gin.H{"undone_card_id": result.CardID})
}

// reviewRender returns both sides of a card.
//
// front and back carry the card-view contract (html, css, body_class, side, media_base), and the frame renders them
// directly. An answer carries the typed value of a type-in card so the back can compare it. edit_href is the review
// page's edit entry.
func (s *Server) reviewRender(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewRenderRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, _, _, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	view, err := s.cardViewWithAnswer(c.Request.Context(), user, schedule.QueueItem{CardID: card.ID}, req.Answer, nil, frameOutputRequested(c))
	if err != nil {
		s.logger.Error("render spa review card failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"card_id": card.PublicID,
		"ordinal": view.Ordinal,
		"front":   view.Front,
		"back":    view.Back,
		"type_in": view.TypeIn,
		// Whether the answer face already shows the question: the page then shows it alone on reveal.
		"answer_includes_question": view.AnswerIncludesQuestion,
		"edit_href":                view.EditHref,
	})
}

// reviewCard validates the review scope and target card of an SPA request: every deck in the scope must be readable (missing one fails the whole request,
// no silent dropping), and the target card must exist and fall inside a deck in the scope. cardPublicID and deckPublicIDs are both public ids,
// always converted to numeric primary keys before the decision; on failure the response has been written and it returns false, on success it returns the parsed scope.
//
// Tags only decide which cards the queue takes and play no part in judging the target card: a grade writes the caller's progress on a readable card, and the card
// being retagged between the two requests should not fail that grade.
func (s *Server) reviewCard(c *gin.Context, user *store.User, deckPublicIDs, tags []string, cardPublicID string) (*store.Card, *store.Note, reviewScope, bool) {
	scope, ok := s.deckScopeFromValues(c, user, deckPublicIDs, tags)
	if !ok {
		return nil, nil, reviewScope{}, false
	}
	card, err := s.cards.ByPublicID(c.Request.Context(), strings.TrimSpace(cardPublicID))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, reviewScope{}, false
	}
	note, err := s.notes.ByID(c.Request.Context(), card.NoteID)
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, reviewScope{}, false
	}
	if len(scope.deckIDs) > 0 {
		inScope := false
		for _, deckID := range scope.deckIDs {
			if note.DeckID == deckID {
				inScope = true
				break
			}
		}
		if !inScope {
			writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return nil, nil, reviewScope{}, false
		}
	}
	return card, note, scope, true
}

// reviewQueueLimit is the bound on the same-scope queue an action response counts over. It equals the largest limit
// GET /api/v1/review/due accepts and the page size the review page requests, so the counts describe exactly the queue
// the client could load; a longer queue is counted up to that bound.
const reviewQueueLimit = 500

// reviewRenderedCard is the response's inline render of the one card it serializes. Its fields mirror the
// POST /api/v1/review/render body, so the client applies it through the same code path as a render response.
type reviewRenderedCard struct {
	CardID                 string   `json:"card_id"`
	Ordinal                int      `json:"ordinal"`
	Front                  CardFace `json:"front"`
	Back                   CardFace `json:"back"`
	TypeIn                 bool     `json:"type_in"`
	AnswerIncludesQuestion bool     `json:"answer_includes_question"`
	EditHref               string   `json:"edit_href"`
}

// writeReviewResult writes the unified response after a grade: the new state, the same-scope queue counts, the next
// card serialized and rendered, plus fields the caller adds (grading feedback / give-up marker). The scope comes back
// verbatim, so the queue cannot degrade to a single deck and tags are not lost.
func (s *Server) writeReviewResult(c *gin.Context, user *store.User, scope reviewScope, result api.SubmitReviewResult, extra ...gin.H) {
	state := gin.H{
		"card_id": result.CardID, "state": result.State,
		"due_at": result.DueAt, "version": result.Version, "stability": result.Stability,
	}
	s.writeQueue(c, user, scope, queueHead, append([]gin.H{state}, extra...)...)
}

// reviewQueuePick selects the one card an action response serializes out of the complete same-scope queue. It may read
// further rows when the card the response must show is outside that queue.
type reviewQueuePick func(ctx context.Context, queue []api.DueCard) []api.DueCard

// writeQueue writes an action response that reports the same-scope queue: the counts over the complete queue plus the
// one card the client should show next, rendered inline when possible. pick selects that card (bury and suspend show
// the queue head; undo shows the card it restored). extra holds fields the caller adds (such as undone_card_id on
// undo), merged into the response body in write order.
//
// The complete queue is still built, because queue_size and new_remaining are totals over it; only one card is
// serialized, so the response body does not grow with the queue.
func (s *Server) writeQueue(c *gin.Context, user *store.User, scope reviewScope, pick reviewQueuePick, extra ...gin.H) {
	queue, err := s.api.DueCards(c.Request.Context(), user, scope.deckIDs, scope.tags, reviewQueueLimit)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	cards := pick(c.Request.Context(), queue)
	body := gin.H{
		"cards": cards,
		// remaining is the total the pre-slim response carried; queue_size names the same total explicitly.
		"remaining":     len(queue),
		"queue_size":    len(queue),
		"new_remaining": newCardsInQueue(queue),
	}
	if rendered, ok := s.inlineCardRender(c, user, cards); ok {
		body["rendered_card"] = rendered
	}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// queueHead is the card an action response shows next: the head of the complete queue, or no card when it is empty.
func queueHead(_ context.Context, queue []api.DueCard) []api.DueCard {
	if len(queue) == 0 {
		return queue[:0]
	}
	return queue[:1]
}

// undoneCard selects the card an undo response shows: the restored card itself. The queue is ordered by due_at, so the
// undone card is not necessarily its head, and serializing the head would hide the card the user just restored.
//
// The restored card can also be outside the queue entirely: undo restores the state and due time the card had before the
// grade, which may be later than today (a card reviewed ahead of its due date, or one that was buried). The response
// still has to carry it, so it is read and assembled on its own through the same rules the queue uses. That read is for
// this response only; queue_size and new_remaining keep counting the real queue, and GET /review/due does not change.
// When even that read fails, the response still reports the write and leaves the card out; undone_card_id names it.
func (s *Server) undoneCard(user *store.User, cardID string) reviewQueuePick {
	return func(ctx context.Context, queue []api.DueCard) []api.DueCard {
		for _, card := range queue {
			if card.CardID == cardID {
				return []api.DueCard{card}
			}
		}
		card, err := s.api.DueCardFor(ctx, user, cardID)
		if err != nil {
			s.logger.Error("load undone card outside the queue failed", "card_id", cardID, "error", err)
			return queue[:0]
		}
		return []api.DueCard{card}
	}
}

// newCardsInQueue counts the never-studied cards in the complete queue. The client cannot derive this number from the
// single card it receives, so the server counts it over the queue it built.
func newCardsInQueue(queue []api.DueCard) int {
	count := 0
	for _, card := range queue {
		if card.State == store.CardStateNew {
			count++
		}
	}
	return count
}

// inlineCardRender renders the card an action response serializes through the same frame-ready path as
// POST /api/v1/review/render, so the client can show it without a second request.
//
// A render failure must not fail the action: when this response is assembled the write has already committed, so a face
// that cannot be produced is logged and left out of the body, and the client falls back to the render endpoint.
func (s *Server) inlineCardRender(c *gin.Context, user *store.User, cards []api.DueCard) (*reviewRenderedCard, bool) {
	if len(cards) == 0 {
		return nil, false
	}
	card, err := s.cards.ByPublicID(c.Request.Context(), cards[0].CardID)
	if err != nil {
		s.logger.Error("resolve card for inline review render failed", "card_id", cards[0].CardID, "error", err)
		return nil, false
	}
	view, err := s.cardViewWithAnswer(c.Request.Context(), user, schedule.QueueItem{CardID: card.ID}, nil, nil, frameOutputRequested(c))
	if err != nil {
		s.logger.Error("render inline review card failed", "card_id", cards[0].CardID, "error", err)
		return nil, false
	}
	return &reviewRenderedCard{
		CardID:                 view.CardID,
		Ordinal:                view.Ordinal,
		Front:                  view.Front,
		Back:                   view.Back,
		TypeIn:                 view.TypeIn,
		AnswerIncludesQuestion: view.AnswerIncludesQuestion,
		EditHref:               view.EditHref,
	}, true
}

// gradeFeedback assembles the grading feedback: the verdict, score and answer text come from the service's grading
// result, and the correct answer is the card's back face rendered with that result. Numeric answers additionally carry
// the parsed number; per-item answers additionally carry the per-item results.
func (s *Server) gradeFeedback(ctx context.Context, user *store.User, card *store.Card, submitted json.RawMessage, grade *api.GradeResult, forFrame bool) (gin.H, error) {
	if grade == nil {
		return nil, errors.New("graded submission returned no grade result")
	}
	result := &answer.GradeResult{
		Score:  grade.Score,
		Rating: grade.Rating,
		Detail: grade.Detail,
		Given:  grade.Given,
	}
	view, err := s.cardFaceView(ctx, user, card, submitted, result, forFrame)
	if err != nil {
		return nil, err
	}
	out := gin.H{
		"verdict":     grade.Verdict,
		"score":       grade.Score,
		"rating":      grade.Rating,
		"answer_html": view.Back.HTML,
		"back":        view.Back,
		"given":       grade.Given,
	}
	if parsed, ok := grade.Detail["parsed_answer"]; ok {
		out["parsed"] = numberText(parsed)
	}
	if breakdown := gradeBreakdown(grade.Detail); breakdown != nil {
		out["breakdown"] = breakdown
	}
	return out, nil
}

// gradeBreakdown turns the per-item part of a grading detail into one entry per item, which the review page marks right
// or wrong. The list control writes "items", the cloze control writes "answers"; both store the acceptable wordings of
// each item and a parallel correctness slice. The recognised keys mirror those two controls; a control without them
// yields no breakdown.
func gradeBreakdown(detail map[string]any) []gin.H {
	items, ok := detailItems(detail)
	if !ok {
		return nil
	}
	correct, _ := detail["correct"].([]bool)
	if len(correct) != len(items) {
		return nil
	}
	canonical, hasCanonical := detail["canonical"].([]string)
	alternatives, hasAlternatives := detail["alternatives"].([][]string)
	separate := hasCanonical && hasAlternatives && len(canonical) == len(items) && len(alternatives) == len(items)
	out := make([]gin.H, 0, len(items))
	for i, wordings := range items {
		entry := gin.H{
			"answer_html": html.EscapeString(strings.Join(wordings, " / ")),
			"correct":     correct[i],
		}
		if separate {
			entry["canonical"] = canonical[i]
			// A nil slice is encoded as null; clients need an array even without alternatives.
			extra := make([]string, len(alternatives[i]))
			copy(extra, alternatives[i])
			entry["alternatives"] = extra
		}
		out = append(out, entry)
	}
	return out
}

// detailItems reads the per-item wording lists out of a grading detail.
func detailItems(detail map[string]any) ([][]string, bool) {
	for _, key := range []string{"items", "answers"} {
		value, ok := detail[key]
		if !ok {
			continue
		}
		items, ok := value.([][]string)
		if ok {
			return items, true
		}
	}
	return nil, false
}

// cardFaceView renders both sides of a card for the SPA, with the submitted answer and its result when there is one.
func (s *Server) cardFaceView(ctx context.Context, user *store.User, card *store.Card, submitted json.RawMessage, result *answer.GradeResult, forFrame bool) (*ReviewCardView, error) {
	return s.cardViewWithAnswer(ctx, user, schedule.QueueItem{CardID: card.ID}, submitted, result, forFrame)
}

// gradeAction* are the three semantics of a grading request (see gradeRequest).
const (
	gradeActionReveal = "reveal"
	gradeActionGiveUp = "give_up"
)

func apiError(err error) *api.ServiceError {
	var se *api.ServiceError
	if errors.As(err, &se) {
		return se
	}
	return &api.ServiceError{Status: http.StatusInternalServerError, Code: api.CodeInternal, Message: api.ErrorMessage(context.Background(), api.CodeInternal)}
}
