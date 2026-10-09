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

// schedulerForDeck 取 userID 复习该卡组时的调度器（由其生效的预设构造）。
func (a *API) schedulerForDeck(ctx context.Context, userID uint64, deck *store.Deck) (*schedule.Scheduler, error) {
	preset, err := a.presetForDeck(ctx, userID, deck)
	if err != nil {
		return nil, err
	}
	return schedule.NewScheduler(preset)
}

// presetForDeck 取 userID 复习该卡组时生效的预设（调度参数与作答题的分数→档位映射都在里面）。
// 属主用卡组上的预设，共享成员用自己的成员设置（store.StudySettings）。
func (a *API) presetForDeck(ctx context.Context, userID uint64, deck *store.Deck) (*store.Preset, error) {
	settings, err := a.decks.StudySettings(ctx, userID, deck)
	if err != nil {
		return nil, err
	}
	return a.presets.ByID(ctx, settings.PresetID)
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
//
// 自评题型给 rating；作答类题型给 answer（服务端判分）或 give_up。grade_source 由服务端决定：
// 请求里出现 self 以外的值直接拒绝，而不是悄悄忽略，免得调用方以为「机器判分」被采纳了。
type submitReviewRequest struct {
	CardID          string          `json:"card_id"`
	Rating          int             `json:"rating"`
	Answer          json.RawMessage `json:"answer"`
	GiveUp          bool            `json:"give_up"`
	ExpectedVersion int             `json:"expected_version"`
	ElapsedMS       *int            `json:"elapsed_ms"`
	GradeSource     string          `json:"grade_source"`
}

// submitReview 提交一次评分；乐观锁不匹配返回 409，scope: review。
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
