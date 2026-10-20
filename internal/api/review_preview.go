package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// RatingInterval 是某一档评分提交后，这张卡到下次复习的间隔预览。
type RatingInterval struct {
	// Rating 取 1–4（Again/Hard/Good/Easy）。
	Rating int `json:"rating"`
	// Seconds 是从现在到下次到期的秒数；学习步骤以分钟计，复习间隔以天计，统一换成秒。
	Seconds int64 `json:"seconds"`
}

// PreviewIntervals 返回 u 对一张卡选每一档评分之后的下次间隔，按 Again/Hard/Good/Easy 排列。
//
// 复习页把它显示在四个评分按钮下面：间隔就是选这一档的后果，比文字说明更能帮人做选择。
// 调度器用 u 在该卡组上生效的预设，与提交评分时同一套参数；预览不加 fuzz（见 schedule.Preview），
// 所以开了 fuzz 的预设，实际间隔可能与显示值有小幅偏移。只读，不写任何状态；至少需要 reader。
func (a *API) PreviewIntervals(ctx context.Context, u *store.User, cardID uint64) ([]RatingInterval, error) {
	card, err := a.cards.ByID(ctx, cardID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
		}
		a.logger.Error("load card for interval preview failed", "card_id", cardID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	note, err := a.notes.ByID(ctx, card.NoteID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
		}
		a.logger.Error("load note for interval preview failed", "card_id", cardID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, note.DeckID, store.RoleReader)
	if err != nil {
		return nil, err
	}
	sched, err := a.schedulerForDeck(ctx, u.ID, deck)
	if err != nil {
		a.logger.Error("load scheduler for interval preview failed", "deck_id", deck.ID, "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	states, err := store.CardStatesByCardIDs(ctx, a.db, u.ID, []uint64{card.ID})
	if err != nil {
		a.logger.Error("load card state for interval preview failed", "card_id", card.ID, "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	// 没有状态行的卡按全新卡处理，与提交评分时的口径一致。
	base := &store.CardState{CardID: card.ID, UserID: u.ID, State: schedule.StateNew.String()}
	if st, ok := states[card.ID]; ok {
		base = &st
	}
	now := a.now()
	outcomes, err := sched.Preview(base, now)
	if err != nil {
		a.logger.Error("preview card intervals failed", "card_id", card.ID, "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	out := make([]RatingInterval, 0, len(outcomes))
	for _, o := range outcomes {
		seconds := int64(o.Due.Sub(now) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		out = append(out, RatingInterval{Rating: int(o.Rating), Seconds: seconds})
	}
	return out, nil
}
