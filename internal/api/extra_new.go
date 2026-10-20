package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// ExtraNewMaxCount 是一次「再来一组」最多追加的新卡张数。它只约束单次请求，防止手误输入
// 一个巨大的数；想学更多可以再追加一次。
const ExtraNewMaxCount = 1000

// ExtraNewStatus 是调用者在一个卡组上今天的新卡额度，以及「再来一组」能加多少。
// REST 与 MCP 共用这一形态。
type ExtraNewStatus struct {
	DeckID string `json:"deck_id"`
	// NewPerDay 是调用者在该卡组上的每日新卡上限（学习目标）；NewUnlimited 为 true 时它无意义。
	NewPerDay    int  `json:"new_per_day"`
	NewUnlimited bool `json:"new_unlimited"`
	// ExtraNew 是今天已经追加的新卡额度，第二天归零。
	ExtraNew int `json:"extra_new"`
	// NewUsed 是今天已经引入的新卡数；NewLeft 是今天还能引入的张数（已含追加量）。
	NewUsed int `json:"new_used"`
	NewLeft int `json:"new_left"`
	// NewAvailable 是卡组里还没学过、此刻可以引入的新卡总数，不受额度限制；
	// 按标签复习时只数带这些标签的卡。
	NewAvailable int `json:"new_available"`
	// SuggestedCount 是「再来一组」的默认张数：每日上限，但不超过今天额度之外还剩的新卡数。
	// 为 0 表示没有可追加的新卡（卡组不限额，或新卡都已在今天的额度里）。
	SuggestedCount int `json:"suggested_count"`
	// MaxCount 是单次追加允许的最大张数（ExtraNewMaxCount）。
	MaxCount int `json:"max_count"`
}

// ExtraNewStatus 返回 u 在卡组上今天的新卡额度与「再来一组」的建议张数（至少 reader）。
// tags 与按标签复习同一规则（见 ReviewTags），只影响 NewAvailable 与 SuggestedCount。
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

// AddExtraNew 为 u 在卡组上追加今天的新卡额度（至少 reader），返回追加后的状态。
//
// count 为 0 表示用默认张数，即调用者在该卡组上的每日新卡上限；否则必须在 1..ExtraNewMaxCount。
// 追加只对调用者自己、只对当前复习日生效：额度写在 (卡组, 用户, 复习日) 上，不改卡组设置，
// 也不影响共享卡组的其他成员。每日新卡不限的卡组没有可抬高的上限，返回 400。
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
	recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionDeckExtraNew,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"count": count, "extra_new": total, "review_day": day},
	})
	return a.extraNewStatus(ctx, u, deck, nil)
}

// extraNewStatus 组装 ExtraNewStatus；额度取自 schedule.DeckBudgets，与复习队列同一条公式。
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
		// 今天额度已经覆盖的新卡不算在「再来一组」里：它们本来就会出现在队列中。
		out.SuggestedCount = min(b.NewPerDay, ExtraNewMaxCount, max(available-b.NewLeft, 0))
	}
	return out, nil
}

// reviewDay 返回 u 此刻所在的复习日，与评分写入 reviews.review_day 的口径一致。
func (a *API) reviewDay(u *store.User) string {
	return schedule.ReviewDay(a.now(), userLocation(u.Timezone), store.ResolveCutoff(u.DayCutoffHour))
}

// getExtraNew 读取今天的新卡额度与「再来一组」的建议张数，scope: review。
// tag 可重复，规则与 GET /review/due 相同。
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

// addExtraNewRequest 是「再来一组」的请求体；count 缺省或为 0 时用每日新卡上限。
type addExtraNewRequest struct {
	Count int `json:"count"`
}

// postExtraNew 为调用者追加今天的新卡额度，scope: review。
func (a *API) postExtraNew(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	var req addExtraNewRequest
	// 空请求体等同于缺省 count；有请求体但不是合法 JSON 才是参数错误。
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
