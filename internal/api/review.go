package api

import (
	"context"
	"net/http"
	"strconv"
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

// userLocation 按用户时区加载 Location；名字非法时退回 UTC（不阻断复习）。
func userLocation(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// dueCards 返回到期卡（含字段原文），scope: review（业务逻辑在 service 层的 DueCards）。
// deck 可重复：缺省＝全部卡组；任一值非数字或为 0 → 400（与既有行为一致）。
func (a *API) dueCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckIDs := make([]uint64, 0, 4)
	for _, raw := range c.QueryArray("deck") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
			return
		}
		deckIDs = append(deckIDs, id)
	}
	cards, err := a.DueCards(c.Request.Context(), u, deckIDs, queryInt(c, "limit", 50))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// submitReviewRequest 是评分提交请求体（DESIGN.md §3.4）。
type submitReviewRequest struct {
	CardID          uint64 `json:"card_id"`
	Rating          int    `json:"rating"`
	ExpectedVersion int    `json:"expected_version"`
	ElapsedMS       *int   `json:"elapsed_ms"`
	GradeSource     string `json:"grade_source"`
}

// submitReview 提交一次评分；乐观锁不匹配返回 409（DESIGN.md §3.4），scope: review。
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
