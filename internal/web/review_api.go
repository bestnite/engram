package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/render"
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
//   - "reveal": only returns the sanitized correct answer, no grading, no DB write (read-only preview before revealing);
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
	if _, graded := graderFor(note.Kind); !graded {
		// A self-assessed type on the wrong entry point: rejected, so a four-grade score cannot pass itself off as machine grading.
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
	feedback, err := s.gradeFeedback(ctx, user, card, note, result.Grade)
	if err != nil {
		s.logger.Error("build spa grade feedback failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	s.writeReviewResult(c, user, scope, result, gin.H{"feedback": feedback})
}

// gradeReveal returns the sanitized correct answer, no grading and no DB write (revealing is a read-only preview).
// After revealing, the frontend switches to the "give up answering" state; only when the user clicks the button again does give_up log Again.
func (s *Server) gradeReveal(c *gin.Context, user *store.User, card *store.Card) {
	answerHTML, err := s.sanitizedBack(c.Request.Context(), user, card)
	if err != nil {
		s.logger.Error("render spa graded answer failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revealed": true, "card_id": card.PublicID, "answer_html": answerHTML})
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
// the queue rebuilt over the same scope.
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
	s.writeQueue(c, user, scope)
}

// reviewSuspend is the SPA entry point for the review page's "suspend this card": session + CSRF protected, the business lives in the service's
// SetCardSuspended (writes only the caller's own progress, reader suffices); the response carries the queue rebuilt over the same scope.
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
	s.writeQueue(c, user, scope)
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
// returns 409 without deleting any grade. Response contract: besides the queue rebuilt over the same scope (cards/remaining), it explicitly carries
// the public id of the undone card (undone_card_id). The queue is ordered by due_at, so the undone card is not guaranteed to be first;
// the frontend must locate the current card by undone_card_id and must not assume it appears first in the queue.
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
	s.writeQueue(c, user, scope, gin.H{"undone_card_id": result.CardID})
}

// reviewRender returns server-sanitized HTML of both sides of a card and of each choice option: the SPA only hands the HTML returned here
// to {@html}, never sending raw fields as Markdown into the HTML sink. It reuses cardView, guaranteeing the same
// goldmark → bluemonday sanitizing path as SSR; edit_href is for the review page's edit entry to jump to;
// intervals are the next interval for each of the four grades (api.PreviewIntervals).
func (s *Server) reviewRender(c *gin.Context) {
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
	card, _, _, ok := s.reviewCard(c, user, req.Deck, req.Tags, req.CardID)
	if !ok {
		return
	}
	view, err := s.cardView(c.Request.Context(), user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		s.logger.Error("render spa review card failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	// The next interval of each of the four grades, shown under the grade buttons. A failed preview only loses this hint line and does not block answering,
	// so we log it and still return the card face as usual (intervals as an empty array).
	intervals, err := s.api.PreviewIntervals(c.Request.Context(), user, card.ID)
	if err != nil {
		s.logger.Warn("preview review intervals failed", "card_id", card.ID, "error", err)
		intervals = []api.RatingInterval{}
	}
	c.JSON(http.StatusOK, gin.H{
		"card_id":      card.PublicID,
		"front_html":   view.FrontHTML,
		"back_html":    view.BackHTML,
		"options_html": view.OptionsHTML,
		"option_order": view.OptionOrder,
		"blanks":       view.Blanks,
		"edit_href":    view.EditHref,
		"intervals":    intervals,
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

// writeReviewResult writes the unified response after a grade: the new state + the queue over the same scope (prefetching the next card),
// plus fields the caller adds (grading feedback / give-up marker). The scope comes back verbatim, so the queue cannot degrade to a single deck and tags are not lost.
func (s *Server) writeReviewResult(c *gin.Context, user *store.User, scope reviewScope, result api.SubmitReviewResult, extra ...gin.H) {
	cards, err := s.api.DueCards(c.Request.Context(), user, scope.deckIDs, scope.tags, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	body := gin.H{"card_id": result.CardID, "state": result.State,
		"due_at": result.DueAt, "version": result.Version, "stability": result.Stability,
		"cards": cards, "remaining": len(cards)}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// writeQueue returns only the queue rebuilt over the same scope (no grade state fields), for actions like bury that produce no reviews row:
// the client moves to the next card from it, and the queue scope cannot degrade to a single deck.
// extra holds fields the caller adds (such as undone_card_id on undo), merged into the response body in write order.
func (s *Server) writeQueue(c *gin.Context, user *store.User, scope reviewScope, extra ...gin.H) {
	cards, err := s.api.DueCards(c.Request.Context(), user, scope.deckIDs, scope.tags, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	body := gin.H{"cards": cards, "remaining": len(cards)}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// gradeFeedback assembles the grading feedback: the verdict, score and answer text come from the service's grading result, the correct answer uses
// server-sanitized HTML; numeric questions additionally carry the parsed number; per-item graded types additionally carry the per-item results.
func (s *Server) gradeFeedback(ctx context.Context, user *store.User, card *store.Card, note *store.Note, grade *api.GradeResult) (gin.H, error) {
	if grade == nil {
		return nil, errors.New("graded submission returned no grade result")
	}
	answerHTML, err := s.sanitizedBack(ctx, user, card)
	if err != nil {
		return nil, err
	}
	out := gin.H{
		"verdict":     grade.Verdict,
		"score":       grade.Score,
		"rating":      grade.Rating,
		"answer_html": answerHTML,
		"given":       grade.Given,
	}
	if parsed, ok := grade.Detail["parsed_answer"]; ok {
		out["parsed"] = numberText(parsed)
	}
	breakdown, err := gradeBreakdown(note.Kind, grade.Detail)
	if err != nil {
		return nil, err
	}
	if breakdown != nil {
		out["breakdown"] = breakdown
	}
	return out, nil
}

// gradeBreakdowner is the optional capability of per-item graded types: turn the grading detail back into per-item results, from which the review page marks each item right or wrong.
type gradeBreakdowner interface {
	Breakdown(detail map[string]any) []cardtype.BreakdownItem
}

// gradeBreakdown renders the per-item results the question type gives into sanitized HTML; returns nil when the type lacks this capability.
// Each item is written as Markdown/TeX source and goes through the same sanitizing pipeline as the card face before reaching the SPA's HTML sink.
func gradeBreakdown(kind string, detail map[string]any) ([]gin.H, error) {
	t, ok := cardtype.Lookup(kind)
	if !ok {
		return nil, nil
	}
	b, ok := t.(gradeBreakdowner)
	if !ok {
		return nil, nil
	}
	items := b.Breakdown(detail)
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		html, err := render.RenderMarkdown(item.Answer)
		if err != nil {
			return nil, err
		}
		out = append(out, gin.H{"answer_html": html, "correct": item.Correct})
	}
	return out, nil
}

// sanitizedBack renders the back side of a card and sanitizes it via internal/render, for the SPA to show the correct answer.
// Reuses cardView, guaranteeing the same sanitizing path as SSR.
func (s *Server) sanitizedBack(ctx context.Context, user *store.User, card *store.Card) (string, error) {
	view, err := s.cardView(ctx, user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		return "", err
	}
	return view.BackHTML, nil
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
