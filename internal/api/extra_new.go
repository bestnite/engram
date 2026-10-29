package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// ExtraNewMaxCount is the most new cards a single "one more batch" may add. It bounds one request only, guarding against a fat-fingered
// huge number; wanting to study more just means adding another batch.
const ExtraNewMaxCount = 1000

// ExtraNewStatus is the caller's new-card allowance on a deck for today, and how much "one more batch" can add.
// REST and MCP share this shape.
type ExtraNewStatus struct {
	DeckID string `json:"deck_id"`
	// NewPerDay is the caller's daily new-card cap on this deck (the study goal); it is meaningless when NewUnlimited is true.
	NewPerDay    int  `json:"new_per_day"`
	NewUnlimited bool `json:"new_unlimited"`
	// ExtraNew is the extra new-card allowance already added today; it resets to zero the next day.
	ExtraNew int `json:"extra_new"`
	// NewUsed is the number of new cards already introduced today; NewLeft is how many can still be introduced today (including the added amount).
	NewUsed int `json:"new_used"`
	NewLeft int `json:"new_left"`
	// NewAvailable is the total number of new cards in the deck not yet studied that could be introduced right now, independent of the allowance;
	// when reviewing by tag only the cards carrying those tags are counted.
	NewAvailable int `json:"new_available"`
	// SuggestedCount is the default count for "one more batch": the daily cap, but no more than the new cards still left beyond today's allowance.
	// Zero means there is no new card to add (the deck has no cap, or the new cards already fit in today's allowance).
	SuggestedCount int `json:"suggested_count"`
	// MaxCount is the maximum count a single add may request (ExtraNewMaxCount).
	MaxCount int `json:"max_count"`
}

// ExtraNewStatus returns u's new-card allowance on the deck for today and the suggested count for "one more batch" (at least reader).
// tags follows the same rules as reviewing by tag (see ReviewTags) and only affects NewAvailable and SuggestedCount.
func (a *API) ExtraNewStatus(ctx context.Context, u *store.User, deckID uint64, tags []string) (ExtraNewStatus, error) {
	tags, err := ReviewTags([]uint64{deckID}, tags)
	if err != nil {
		return ExtraNewStatus{}, err
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, deckID, store.RoleReader)
	if err != nil {
		return ExtraNewStatus{}, err
	}
	return a.extraNewStatus(ctx, u, deck, tags)
}

// AddExtraNew adds to u's new-card allowance on the deck for today (at least reader) and returns the status after the add.
//
// count of 0 means the default count, i.e. the caller's daily new-card cap on this deck; otherwise it must be within 1..ExtraNewMaxCount.
// The add applies only to the caller and only to the current review day: the allowance is stored per (deck, user, review day), the deck settings are untouched,
// and other members of a shared deck are unaffected. A deck with unlimited daily new cards has no cap to raise, so it returns 400.
func (a *API) AddExtraNew(ctx context.Context, u *store.User, apiKeyID *uint64, deckID uint64, count int) (ExtraNewStatus, error) {
	if count < 0 || count > ExtraNewMaxCount {
		return ExtraNewStatus{}, InvalidRequest("count must be between 1 and 1000, or omitted for the daily new card limit")
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, deckID, store.RoleReader)
	if err != nil {
		return ExtraNewStatus{}, err
	}
	settings, err := a.decks.StudySettings(ctx, u.ID, deck)
	if err != nil {
		a.logger.Error("load study settings for extra new failed", "deck_id", deck.ID, "user_id", u.ID, "error", err)
		return ExtraNewStatus{}, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	if settings.Caps.NewPerDay <= 0 {
		return ExtraNewStatus{}, InvalidRequest("the deck has no daily new card limit to extend")
	}
	if count == 0 {
		count = min(settings.Caps.NewPerDay, ExtraNewMaxCount)
	}
	day := a.reviewDay(u)
	total, err := store.AddDailyExtraNew(ctx, a.db, deck.ID, u.ID, day, count)
	if err != nil {
		a.logger.Error("add extra new cards failed", "deck_id", deck.ID, "user_id", u.ID, "error", err)
		return ExtraNewStatus{}, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionDeckExtraNew,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"count": count, "extra_new": total, "review_day": day},
	})
	return a.extraNewStatus(ctx, u, deck, nil)
}

// extraNewStatus assembles ExtraNewStatus; the allowance comes from schedule.DeckBudgets, the same formula as the review queue.
func (a *API) extraNewStatus(ctx context.Context, u *store.User, deck *store.Deck, tags []string) (ExtraNewStatus, error) {
	now := a.now()
	builder := schedule.NewQueueBuilder(a.db, a.decks, nil)
	budgets, err := builder.DeckBudgets(ctx, u.ID, []uint64{deck.ID}, schedule.QueueOptions{
		Now: now, Timezone: u.Timezone, DayCutoffHour: u.DayCutoffHour,
	})
	if err != nil {
		a.logger.Error("load deck budget for extra new failed", "deck_id", deck.ID, "user_id", u.ID, "error", err)
		return ExtraNewStatus{}, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	available, err := builder.NewCardsAvailable(ctx, u.ID, deck.ID, tags, now)
	if err != nil {
		a.logger.Error("count available new cards failed", "deck_id", deck.ID, "user_id", u.ID, "error", err)
		return ExtraNewStatus{}, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	b := budgets[deck.ID]
	out := ExtraNewStatus{
		DeckID:       deck.PublicID,
		NewPerDay:    b.NewPerDay,
		NewUnlimited: b.NewUnlimited,
		ExtraNew:     b.NewExtra,
		NewUsed:      b.NewUsed,
		NewLeft:      b.NewLeft,
		NewAvailable: available,
		MaxCount:     ExtraNewMaxCount,
	}
	if !b.NewUnlimited {
		// New cards already covered by today's allowance do not count toward "one more batch": they show up in the queue anyway.
		out.SuggestedCount = min(b.NewPerDay, ExtraNewMaxCount, max(available-b.NewLeft, 0))
	}
	return out, nil
}

// reviewDay returns the review day u is in right now, consistent with what grading writes to reviews.review_day.
func (a *API) reviewDay(u *store.User) string {
	return reviewday.Day(a.now(), reviewday.Location(u.Timezone), reviewday.ResolveCutoff(u.DayCutoffHour))
}

// getExtraNew reads today's new-card allowance and the suggested count for "one more batch", scope: review.
// tag is repeatable, with the same rules as GET /review/due.
func (a *API) getExtraNew(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	status, err := a.ExtraNewStatus(ctx, u, d.ID, c.QueryArray("tag"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

// addExtraNewRequest is the "one more batch" body; when count is absent or 0 the daily new-card cap is used.
type addExtraNewRequest struct {
	Count int `json:"count"`
}

// postExtraNew adds to the caller's new-card allowance for today, scope: review.
func (a *API) postExtraNew(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	var req addExtraNewRequest
	// An empty body is the same as an absent count; only a body that is present but not valid JSON is a parameter error.
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
			return
		}
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	status, err := a.AddExtraNew(ctx, u, CurrentAPIKeyID(c), d.ID, req.Count)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}
