package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// UndoReviewInput 是一次撤销的入参：CardID 是要撤销最后一次评分的卡的对外 id。
type UndoReviewInput struct {
	CardID string
	// ExpectedVersion 是调用方读到的 card_states.version，通常是它刚提交评分后得到的版本。
	// 撤销据此绑定目标评分：不匹配返回 409，且不删任何评分——重放请求与双开窗口因此不会
	// 误删另一条历史评分。
	ExpectedVersion int
}

// UndoReviewResult 是撤销的响应形态。
//
// CardID 是被撤销卡的对外 id：撤销会把这张卡放回队列，而队列按 due_at 排序，
// 它不保证排在首位，调用方必须据此字段（而不是队列顺序）把当前卡定位回它。
// 其余字段是恢复后的状态，供调用方展示或继续调度。
type UndoReviewResult struct {
	CardID    string     `json:"card_id"`
	State     string     `json:"state"`
	DueAt     *time.Time `json:"due_at"`
	Version   int        `json:"version"`
	Stability *float64   `json:"stability"`
}

// UndoReview 撤销一张卡的最后一次评分：恢复 card_states、删除被撤销的 reviews 行，
// 并写一条审计行。全部业务逻辑在 schedule.Rollback 里，这里只做身份解析与事务边界
// （一种业务逻辑、两条传输：REST 与 MCP 共用这一份实现）。
//
// 判权用 reader：撤销写的是调用者本人的 (card_id, user_id) 进度，共享卡组的读者可以
// 撤销自己刚做的评分，不影响属主与其他成员。卡组范围由调用方先行整次校验（web 层
// 复用 reviewCard），这里再对目标卡所在卡组要求 reader 作为兜底。
//
// 该卡没有任何复习日志时返回 409 conflict（ErrNothingToUndo）；状态行在读取后被并发
// 改过时返回 409 version_conflict（ErrVersionConflict），与 SubmitReview 同口径。
func (a *API) UndoReview(ctx context.Context, u *store.User, in UndoReviewInput) (UndoReviewResult, error) {
	if strings.TrimSpace(in.CardID) == "" {
		return UndoReviewResult{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "card_id is required")
	}
	card, err := a.cards.ByPublicID(ctx, in.CardID)
	if err != nil {
		return UndoReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "card not found")
	}
	note, err := a.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return UndoReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, note.DeckID, store.RoleReader)
	if err != nil {
		return UndoReviewResult{}, err
	}
	sched, err := a.schedulerForDeck(ctx, u.ID, deck)
	if err != nil {
		return UndoReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
	}

	var restored store.CardState
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inner error
		restored, inner = schedule.Rollback(ctx, tx, schedule.UndoInput{
			CardID:          card.ID,
			UserID:          u.ID,
			ExpectedVersion: in.ExpectedVersion,
			Scheduler:       sched,
			Now:             a.now(),
		})
		return inner
	})
	if err != nil {
		switch {
		case errors.Is(err, schedule.ErrNothingToUndo):
			return UndoReviewResult{}, newServiceError(http.StatusConflict, CodeConflict, "no review to undo")
		case errors.Is(err, schedule.ErrVersionConflict):
			return UndoReviewResult{}, newServiceError(http.StatusConflict, CodeVersionConflict, "card state version conflict")
		}
		a.logger.Error("undo review failed", "card_id", card.ID, "user_id", u.ID, "error", err)
		return UndoReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to undo review")
	}
	return UndoReviewResult{
		CardID:    card.PublicID,
		State:     restored.State,
		DueAt:     restored.DueAt,
		Version:   restored.Version,
		Stability: restored.Stability,
	}, nil
}
