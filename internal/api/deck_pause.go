package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// DeckPauseStatus is the response of pausing or resuming a deck; REST and MCP share the shape.
type DeckPauseStatus struct {
	DeckID string `json:"deck_id"`
	Paused bool   `json:"paused"`
}

// SetDeckPaused pauses or resumes a deck for u only (at least reader); REST and the built-in MCP share this one implementation.
//
// Pausing is the caller's own study choice: the owner and every member decide for themselves, and nobody else's queue changes.
// A paused deck leaves the default review queue, the due count of the stats summary, the unfiltered due forecast and the reminder
// mail; naming it explicitly still studies it. Both directions are idempotent, and an audit row is written only when the state changed.
func (a *API) SetDeckPaused(ctx context.Context, u *store.User, apiKeyID *uint64, deckID uint64, paused bool) (DeckPauseStatus, error) {
	deck, err := a.RequireDeckRole(ctx, u.ID, deckID, store.RoleReader)
	if err != nil {
		return DeckPauseStatus{}, err
	}
	changed, err := a.decks.SetPaused(ctx, u.ID, deck.ID, paused, a.now())
	if err != nil {
		a.logger.Error("set deck paused failed", "deck_id", deck.ID, "user_id", u.ID, "paused", paused, "error", err)
		return DeckPauseStatus{}, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	if changed {
		action := store.ActionDeckResume
		if paused {
			action = store.ActionDeckPause
		}
		recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
			UserID:     store.Ptr(u.ID),
			APIKeyID:   apiKeyID,
			Action:     action,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
		})
	}
	return DeckPauseStatus{DeckID: deck.PublicID, Paused: paused}, nil
}

// setDeckPausedRequest is the body of PUT /decks/:id/paused. paused is a pointer so that an absent field is rejected instead of
// silently resuming the deck.
type setDeckPausedRequest struct {
	Paused *bool `json:"paused"`
}

// putDeckPaused pauses or resumes a deck for the caller, scope: review (the logic is in the service layer's SetDeckPaused).
func (a *API) putDeckPaused(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	var req setDeckPausedRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Paused == nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	status, err := a.SetDeckPaused(ctx, u, CurrentAPIKeyID(c), d.ID, *req.Paused)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}
