package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// schedulerForDeck 取卡组的调度器（由卡组预设构造）。
func (a *API) schedulerForDeck(ctx context.Context, deck *store.Deck) (*schedule.Scheduler, error) {
	preset, err := a.presets.ByID(ctx, deck.PresetID)
	if err != nil {
		return nil, err
	}
	return schedule.NewScheduler(preset)
}

// userLocation 按用户时区加载 Location；回退规则见 store.LoadLocation（空/非法名 → UTC）。
func userLocation(tz string) *time.Location {
	return store.LoadLocation(tz)
}

// dueCards 返回到期卡（含字段原文），scope: review（业务逻辑在 service 层的 DueCards）。
// deck 可重复：缺省＝全部卡组；每个值是卡组的对外 id，未知或非法 → 404。
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
	cards, err := a.DueCards(ctx, u, deckIDs, queryInt(c, "limit", 50))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// submitReviewRequest 是评分提交请求体；card_id 是卡的对外 id（不透明字符串）。
type submitReviewRequest struct {
	CardID          string `json:"card_id"`
	Rating          int    `json:"rating"`
	ExpectedVersion int    `json:"expected_version"`
	ElapsedMS       *int   `json:"elapsed_ms"`
	GradeSource     string `json:"grade_source"`
}

// submitReview 提交一次评分；乐观锁不匹配返回 409，scope: review。
func (a *API) submitReview(c *gin.Context) {
	u, _ := CurrentUser(c)
	var req submitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	result, err := a.SubmitReview(c.Request.Context(), u, CurrentAPIKeyID(c), SubmitReviewInput{
		CardID:          req.CardID,
		Rating:          req.Rating,
		ExpectedVersion: req.ExpectedVersion,
		ElapsedMS:       req.ElapsedMS,
		GradeSource:     req.GradeSource,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
