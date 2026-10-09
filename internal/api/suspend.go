package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// 暂停与恢复：每个用户对自己的卡做的决定，只写调用者自己的 card_states.suspended_at。
// 读者也可以暂停（与埋藏一样，它写的是本人进度）；REST、网页复习页与卡组笔记列表都经过这里。

// SuspensionResult 是暂停 / 恢复的结果；Cards 是受影响的卡（对外 id）。
type SuspensionResult struct {
	Suspended bool     `json:"suspended"`
	Cards     []string `json:"cards"`
}

// SetCardSuspended 暂停或恢复调用者的一张卡。
func (a *API) SetCardSuspended(ctx context.Context, u *store.User, apiKeyID *uint64, cardPublicID string, suspended bool) (SuspensionResult, error) {
	card, err := a.cards.ByPublicID(ctx, strings.TrimSpace(cardPublicID))
	if err != nil {
		return SuspensionResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "card not found")
	}
	if _, _, err := a.RequireNoteRole(ctx, u.ID, card.NoteID, store.RoleReader); err != nil {
		return SuspensionResult{}, err
	}
	return a.setSuspended(ctx, u, apiKeyID, []store.Card{*card}, suspended)
}

// SetNoteSuspended 暂停或恢复调用者在一条 note 下的全部卡（卡组笔记列表的「暂停 / 取消暂停」）。
func (a *API) SetNoteSuspended(ctx context.Context, u *store.User, apiKeyID *uint64, notePublicID string, suspended bool) (SuspensionResult, error) {
	note, err := a.notes.ByPublicID(ctx, strings.TrimSpace(notePublicID))
	if err != nil {
		return SuspensionResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	if _, _, err := a.RequireNoteRole(ctx, u.ID, note.ID, store.RoleReader); err != nil {
		return SuspensionResult{}, err
	}
	cards, err := a.cards.ByNote(ctx, note.ID)
	if err != nil {
		return SuspensionResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load cards")
	}
	return a.setSuspended(ctx, u, apiKeyID, cards, suspended)
}

// setSuspended 在一个事务里改写这些卡的暂停状态，并逐卡留审计。
func (a *API) setSuspended(ctx context.Context, u *store.User, apiKeyID *uint64, cards []store.Card, suspended bool) (SuspensionResult, error) {
	out := SuspensionResult{Suspended: suspended, Cards: make([]string, 0, len(cards))}
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, c := range cards {
			if _, err := schedule.SetSuspended(ctx, tx, schedule.SuspendInput{
				CardID: c.ID, UserID: u.ID, Suspended: suspended, Now: a.now(),
			}); err != nil {
				return err
			}
			out.Cards = append(out.Cards, c.PublicID)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, schedule.ErrVersionConflict) {
			return SuspensionResult{}, newServiceError(http.StatusConflict, CodeVersionConflict, "card state version conflict")
		}
		a.logger.Error("set card suspension failed", "user_id", u.ID, "error", err)
		return SuspensionResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to change suspension")
	}
	action := store.ActionCardUnsuspend
	if suspended {
		action = store.ActionCardSuspend
	}
	for _, c := range cards {
		recordAudit(ctx, a.auditor, a.logger, store.AuditEntry{
			UserID: store.Ptr(u.ID), APIKeyID: apiKeyID, Action: action,
			TargetType: "card", TargetID: store.Ptr(c.ID),
		})
	}
	return out, nil
}

// putCardSuspension / deleteCardSuspension 是 PUT / DELETE /api/v1/cards/:id/suspension（scope: review）。
func (a *API) putCardSuspension(c *gin.Context)    { a.cardSuspension(c, true) }
func (a *API) deleteCardSuspension(c *gin.Context) { a.cardSuspension(c, false) }

func (a *API) cardSuspension(c *gin.Context, suspended bool) {
	u, _ := CurrentUser(c)
	id, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	res, err := a.SetCardSuspended(c.Request.Context(), u, CurrentAPIKeyID(c), id, suspended)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// putNoteSuspension / deleteNoteSuspension 是 PUT / DELETE /api/v1/notes/:id/suspension（scope: review）。
func (a *API) putNoteSuspension(c *gin.Context)    { a.noteSuspension(c, true) }
func (a *API) deleteNoteSuspension(c *gin.Context) { a.noteSuspension(c, false) }

func (a *API) noteSuspension(c *gin.Context, suspended bool) {
	u, _ := CurrentUser(c)
	id, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	res, err := a.SetNoteSuspended(c.Request.Context(), u, CurrentAPIKeyID(c), id, suspended)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
