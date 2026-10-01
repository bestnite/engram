package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/schedule"
	"example.com/flashcard/internal/store"
)

// statsSummary 返回到期量 / 复习量 / 留存概要，scope: read（DESIGN.md §7.3、§9）。
// 所有数字都由 reviews + card_states 聚合而来，不引入额外数据源。
func (a *API) statsSummary(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	now := a.now()

	decks, err := a.decks.ListByOwner(ctx, u.ID)
	if err != nil {
		a.logger.Error("load decks for stats failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}
	deckIDs := make([]uint64, 0, len(decks))
	for _, d := range decks {
		deckIDs = append(deckIDs, d.ID)
	}

	resp := gin.H{
		"decks":         len(decks),
		"due":           int64(0),
		"reviews_today": int64(0),
		"reviews_total": int64(0),
		"retention":     float64(0),
		"notes":         int64(0),
		"cards":         int64(0),
	}
	if len(deckIDs) == 0 {
		c.JSON(http.StatusOK, resp)
		return
	}

	var due int64
	if err := a.db.WithContext(ctx).Model(&store.CardState{}).
		Joins("JOIN cards ON cards.id = card_states.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).
		Where("card_states.user_id = ? AND card_states.due_at IS NOT NULL AND card_states.due_at <= ?", u.ID, now).
		Count(&due).Error; err != nil {
		a.logger.Error("count due cards failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}

	day := schedule.ReviewDay(now, userLocation(u.Timezone), u.DayCutoffHour)
	var reviewsToday int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND review_day = ?", u.ID, day).Count(&reviewsToday).Error; err != nil {
		a.logger.Error("count reviews today failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}
	var reviewsTotal int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ?", u.ID).Count(&reviewsTotal).Error; err != nil {
		a.logger.Error("count reviews failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}
	// 留存近似口径：非 Again 的比例（更精细的分桶留给 M7）。
	nonAgain, err := countNonAgain(ctx, a, u.ID)
	if err != nil {
		a.logger.Error("count retention failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}

	var notes, cards int64
	if err := a.db.WithContext(ctx).Model(&store.Note{}).
		Where("deck_id IN ?", deckIDs).Count(&notes).Error; err != nil {
		a.logger.Error("count notes failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}
	if err := a.db.WithContext(ctx).Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).Where("cards.deleted_at IS NULL").
		Count(&cards).Error; err != nil {
		a.logger.Error("count cards failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load statistics")
		return
	}

	retention := 0.0
	if reviewsTotal > 0 {
		retention = float64(nonAgain) / float64(reviewsTotal)
	}
	resp["due"] = due
	resp["reviews_today"] = reviewsToday
	resp["reviews_total"] = reviewsTotal
	resp["retention"] = retention
	resp["notes"] = notes
	resp["cards"] = cards
	c.JSON(http.StatusOK, resp)
}

// countNonAgain 统计该用户 rating != 1 的复习条数，作为留存率分子。
func countNonAgain(ctx context.Context, a *API, userID uint64) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND rating > 1", userID).Count(&n).Error
	return n, err
}
