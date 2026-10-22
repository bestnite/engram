package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// schedulerForDeck returns the scheduler for userID reviewing this deck (built from their effective preset).
func (a *API) schedulerForDeck(ctx context.Context, userID uint64, deck *store.Deck) (*schedule.Scheduler, error) {
	preset, err := a.presetForDeck(ctx, userID, deck)
	if err != nil {
		return nil, err
	}
	return schedule.NewScheduler(preset)
}

// presetForDeck returns the preset in effect for userID reviewing this deck (both the scheduling parameters and the score -> rating mapping for answer cards live in it).
// Owners use the preset on the deck, shared members use their own member settings (store.StudySettings).
func (a *API) presetForDeck(ctx context.Context, userID uint64, deck *store.Deck) (*store.Preset, error) {
	settings, err := a.decks.StudySettings(ctx, userID, deck)
	if err != nil {
		return nil, err
	}
	return a.presets.ByID(ctx, settings.PresetID)
}

// userLocation loads the Location for the user's timezone; fallback rules in store.LoadLocation (empty/invalid name -> UTC).
func userLocation(tz string) *time.Location {
	return store.LoadLocation(tz)
}

// dueCards returns the due cards (with raw field text), scope: review (the logic lives in the service layer's DueCards).
// deck is repeatable: absent means all decks; each value is a deck's external id, unknown or malformed -> 404.
// tag is repeatable: only cards carrying any one of these tags are taken; when tags are given there must be exactly one deck, otherwise 400.
func (a *API) dueCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	deckIDs := make([]uint64, 0, 4)
	for _, raw := range c.QueryArray("deck") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		d, err := a.decks.ByPublicID(ctx, raw)
		if err != nil {
			abortNotFound(c)
			return
		}
		deckIDs = append(deckIDs, d.ID)
	}
	cards, err := a.DueCards(ctx, u, deckIDs, c.QueryArray("tag"), queryInt(c, "limit", 50))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// submitReviewRequest is the grade submission body; card_id is the card's external id (an opaque string).
//
// Self-graded card types send rating; answer card types send answer (server-side grading) or give_up. grade_source is decided by the server:
// any value other than self in the request is rejected outright rather than silently ignored, so the caller does not believe a "machine grade" was accepted.
type submitReviewRequest struct {
	CardID          string          `json:"card_id"`
	Rating          int             `json:"rating"`
	Answer          json.RawMessage `json:"answer"`
	GiveUp          bool            `json:"give_up"`
	ExpectedVersion int             `json:"expected_version"`
	ElapsedMS       *int            `json:"elapsed_ms"`
	GradeSource     string          `json:"grade_source"`
}

// submitReview submits one grade; an optimistic-lock mismatch returns 409, scope: review.
func (a *API) submitReview(c *gin.Context) {
	u, _ := CurrentUser(c)
	var req submitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	if req.GradeSource != "" && req.GradeSource != schedule.GradeSourceSelf {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "grade_source is decided by the server")
		return
	}
	result, err := a.SubmitReview(c.Request.Context(), u, CurrentAPIKeyID(c), SubmitReviewInput{
		CardID:          req.CardID,
		Rating:          req.Rating,
		Answer:          req.Answer,
		GiveUp:          req.GiveUp,
		ExpectedVersion: req.ExpectedVersion,
		ElapsedMS:       req.ElapsedMS,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
