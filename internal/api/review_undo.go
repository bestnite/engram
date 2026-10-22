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

// UndoReviewInput is the input of one undo: CardID is the external id of the card whose last grade is being undone.
type UndoReviewInput struct {
	CardID string
	// ExpectedVersion is the card_states.version the caller read, usually the one it got right after submitting a grade.
	// The undo binds its target grade on it: a mismatch returns 409 and deletes no grade at all - so a replayed request or a double-open window cannot
	// wrongly delete another historical grade.
	ExpectedVersion int
}

// UndoReviewResult is the response shape of an undo.
//
// CardID is the external id of the undone card: an undo puts the card back in the queue, and the queue is ordered by due_at,
// so it is not guaranteed to come first; the caller must relocate the current card using this field (not the queue order).
// The remaining fields are the restored state, for the caller to display or keep scheduling with.
type UndoReviewResult struct {
	CardID    string     `json:"card_id"`
	State     string     `json:"state"`
	DueAt     *time.Time `json:"due_at"`
	Version   int        `json:"version"`
	Stability *float64   `json:"stability"`
}

// UndoReview undoes the last grade of a card: it restores card_states, deletes the undone reviews rows,
// and writes one audit row. All the logic is in schedule.Rollback; this only does identity resolution and the transaction boundary
// (one piece of logic, two transports: REST and MCP share this single implementation).
//
// Authorization uses reader: an undo writes the caller's own (card_id, user_id) progress, so readers of a shared deck can
// undo a grade they just made without affecting the owner or other members. Deck scope is validated wholesale by the caller first (the web layer
// reuses reviewCard), and reader on the target card's deck is required here as a backstop.
//
// When the card has no review log at all it returns 409 conflict (ErrNothingToUndo); when the state row was concurrently
// modified after being read it returns 409 version_conflict (ErrVersionConflict), the same convention as SubmitReview.
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
