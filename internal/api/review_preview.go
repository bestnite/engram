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

// RatingInterval is the preview of the interval until this card's next review once a given rating is submitted.
type RatingInterval struct {
	// Rating is 1-4 (Again/Hard/Good/Easy).
	Rating int `json:"rating"`
	// Seconds is the number of seconds from now until the next due time; learning steps count in minutes and review intervals in days, both converted to seconds.
	Seconds int64 `json:"seconds"`
}

// PreviewIntervals returns, for u, the next interval after choosing each rating for a card, ordered Again/Hard/Good/Easy.
//
// The review page shows it under the four rating buttons: the interval is the consequence of picking that rating, and helps people choose better than any wording.
// The scheduler uses u's effective preset on this deck, the same parameter set as when submitting a grade; the preview adds no fuzz (see schedule.Preview),
// so with fuzz enabled the actual interval may drift slightly from the shown value. Read-only, writes no state; requires at least reader.
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
	// A card with no state row is treated as brand new, consistent with grade submission.
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
