package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// BuryResult is the outcome of burying a card: the card (external id) and the instant it returns to the queue.
type BuryResult struct {
	CardID string    `json:"card_id"`
	DueAt  time.Time `json:"due_at"`
}

// BuryCard postpones one of the caller's cards to the start of their next review day, so it leaves today's queue without
// any change to its progress and writes no review. The next review day is computed in the caller's own timezone and day
// cutoff: computed in UTC, a card buried in the morning at UTC+8 would come back at noon the same day.
//
// Reader suffices: what is written is the caller's own card_states row, so a reader of a shared deck can review on
// their own. REST, MCP and the web review page all go through here.
func (a *API) BuryCard(ctx context.Context, u *store.User, cardPublicID string) (BuryResult, error) {
	card, err := a.cards.ByPublicID(ctx, strings.TrimSpace(cardPublicID))
	if err != nil {
		return BuryResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "card not found")
	}
	if _, _, err := a.RequireNoteRole(ctx, u.ID, card.NoteID, store.RoleReader); err != nil {
		return BuryResult{}, err
	}
	var row store.CardState
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var berr error
		row, berr = schedule.Bury(ctx, tx, schedule.BuryInput{
			CardID: card.ID, UserID: u.ID, Now: a.now(),
			Timezone: u.Timezone, DayCutoffHour: u.DayCutoffHour,
		})
		return berr
	})
	if err != nil {
		if errors.Is(err, schedule.ErrVersionConflict) {
			return BuryResult{}, newServiceError(http.StatusConflict, CodeVersionConflict, "card state version conflict")
		}
		a.logger.Error("bury card failed", "user_id", u.ID, "card_id", card.ID, "error", err)
		return BuryResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to bury card")
	}
	out := BuryResult{CardID: card.PublicID}
	if row.DueAt != nil {
		out.DueAt = row.DueAt.UTC()
	}
	return out, nil
}

// postCardBury is POST /api/v1/cards/:id/bury (scope: review).
func (a *API) postCardBury(c *gin.Context) {
	u, _ := CurrentUser(c)
	id, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	res, err := a.BuryCard(c.Request.Context(), u, id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
