package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// EmptyCardJSON is one card the empty-card cleanup found.
//
// id is the card's public id and note_id / note_type_id are the public ids of its note and note type, so a client can
// report what would disappear without a second lookup.
type EmptyCardJSON struct {
	ID         string `json:"id"`
	NoteID     string `json:"note_id"`
	NoteTypeID string `json:"note_type_id"`
	Template   string `json:"template"`
	Ordinal    int    `json:"ordinal"`
}

// EmptyCardListResponse is the body of GET /api/v1/decks/:id/empty-cards. count repeats len(cards) so a caller that
// only wants the number does not walk the list.
type EmptyCardListResponse struct {
	Cards []EmptyCardJSON `json:"cards"`
	Count int             `json:"count"`
}

// CleanupEmptyCardsInput carries the two safety switches of the empty-card cleanup. DryRun reports the counts and
// writes nothing; ConfirmCardRemoval allows the cards to be soft-deleted.
type CleanupEmptyCardsInput struct {
	DryRun             bool
	ConfirmCardRemoval bool
}

// CleanupEmptyCardsResult is what the cleanup did. cards_removed counts the soft-deleted cards;
// reviews_on_removed_cards counts the review rows those cards keep, because the review log is never destroyed.
type CleanupEmptyCardsResult struct {
	DryRun                bool `json:"dry_run"`
	CardsRemoved          int  `json:"cards_removed"`
	ReviewsOnRemovedCards int  `json:"reviews_on_removed_cards"`
}

// emptyCardCleanupUnconfirmedResponse is the 409 body of an unconfirmed cleanup: the standard error envelope plus the
// counts, so the caller sees exactly what confirming would remove. It mirrors the note type update's response.
type emptyCardCleanupUnconfirmedResponse struct {
	Error                 errorBody `json:"error"`
	CardsRemoved          int       `json:"cards_removed"`
	ReviewsOnRemovedCards int       `json:"reviews_on_removed_cards"`
}

// ChangeNoteTypeInput is the input of a change-note-type operation.
//
// TargetNoteType is the public id of a note type of the same deck. FieldMapping maps a source field id to a target
// field id; TemplateMapping maps a source template id to a target template id. A target field with no mapping receives
// an empty value, and a source template with no mapping has its cards soft-deleted, which needs ConfirmCardRemoval.
type ChangeNoteTypeInput struct {
	TargetNoteType     string
	FieldMapping       map[string]string
	TemplateMapping    map[string]string
	DryRun             bool
	ConfirmCardRemoval bool
}

// ChangeNoteTypeResult is what a change-note-type operation did or, for a dry run, would do.
//
// notes_moved is the number of notes whose note type changed; cards_moved counts cards that kept their id and progress,
// whether their template was mapped or their unmapped template id is declared by the target type; cards_added counts
// cards that became visible because the target note type generates their template for the note, either newly inserted
// or restored from a soft-deleted row; cards_removed counts cards of unmapped templates the target type does not
// declare, which were soft-deleted, together with the review rows they keep. A card is never counted in both
// cards_added and cards_removed.
type ChangeNoteTypeResult struct {
	DryRun                bool `json:"dry_run"`
	NotesMoved            int  `json:"notes_moved"`
	CardsMoved            int  `json:"cards_moved"`
	CardsAdded            int  `json:"cards_added"`
	CardsRemoved          int  `json:"cards_removed"`
	ReviewsOnRemovedCards int  `json:"reviews_on_removed_cards"`
}

// ListEmptyCards reports the cards of one deck whose front renders empty (scope: read, editor role).
//
// Listing is the first half of the cleanup, so it takes the same editor role the cleanup needs: a reader has nothing
// to do with the result.
func (a *API) ListEmptyCards(ctx context.Context, userID, deckID uint64) ([]EmptyCardJSON, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return nil, err
	}
	cards, err := a.notes.ListEmptyCards(ctx, d.ID)
	if err != nil {
		a.logger.Error("list empty cards failed", "deck_id", d.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list empty cards")
	}
	out := make([]EmptyCardJSON, 0, len(cards))
	for _, card := range cards {
		out = append(out, EmptyCardJSON{
			ID:         card.CardPublicID,
			NoteID:     card.NotePublicID,
			NoteTypeID: card.NoteTypePublicID,
			Template:   card.Template,
			Ordinal:    card.Ordinal,
		})
	}
	return out, nil
}

// CleanupEmptyCards soft-deletes the cards of one deck whose front renders empty (scope: write, editor role).
//
// A soft-delete keeps the card row, so card_states and reviews survive and only the card leaves the queue. A dry run
// writes nothing and an unconfirmed cleanup is refused with 409 card_removal_unconfirmed plus the counts; a successful
// cleanup writes one audit row.
func (a *API) CleanupEmptyCards(ctx context.Context, userID, deckID uint64, apiKeyID *uint64, in CleanupEmptyCardsInput) (CleanupEmptyCardsResult, error) {
	result := CleanupEmptyCardsResult{DryRun: in.DryRun}
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return result, err
	}
	impact, err := a.notes.CleanupEmptyCards(ctx, d.ID, store.EmptyCardCleanupOptions{
		DryRun:  in.DryRun,
		Confirm: in.ConfirmCardRemoval,
	})
	result.CardsRemoved = impact.CardsRemoved
	result.ReviewsOnRemovedCards = impact.ReviewsOnRemovedCards
	if err != nil {
		if errors.Is(err, store.ErrCardRemovalUnconfirmed) {
			return result, newServiceError(http.StatusConflict, CodeCardRemovalUnconfirmed, "")
		}
		a.logger.Error("cleanup empty cards failed", "deck_id", d.ID, "error", err)
		return result, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to clean up empty cards")
	}
	if in.DryRun || impact.CardsRemoved == 0 {
		return result, nil
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionCardEmptyCleanup,
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail: map[string]any{
			"deck_id":                  d.PublicID,
			"cards_removed":            impact.CardsRemoved,
			"reviews_on_removed_cards": impact.ReviewsOnRemovedCards,
		},
	})
	return result, nil
}

// ChangeNoteType moves a note type's notes to another note type of the same deck (scope: write, editor role).
//
// A target note type of another deck is reported as not found, never as a forbidden resource: the endpoint does not
// confirm note types outside the deck. A kind change and a mapping id the configs do not declare are 400
// invalid_request naming the offending id. An operation that would soft-delete cards without ConfirmCardRemoval is
// 409 card_removal_unconfirmed with the counts; a successful operation writes one audit row.
func (a *API) ChangeNoteType(ctx context.Context, userID, deckID uint64, sourcePublicID string, apiKeyID *uint64, in ChangeNoteTypeInput) (ChangeNoteTypeResult, error) {
	result := ChangeNoteTypeResult{DryRun: in.DryRun}
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return result, err
	}
	source, err := a.noteTypeInDeck(ctx, d.ID, sourcePublicID)
	if err != nil {
		return result, err
	}
	target, err := a.noteTypes.ByPublicID(ctx, in.TargetNoteType)
	if err != nil || target.DeckID != d.ID {
		return result, newServiceError(http.StatusNotFound, CodeNotFound, "target note type not found")
	}
	impact, err := a.notes.ChangeNoteType(ctx, d.ID, source.ID, target.ID, store.NoteTypeChangeOptions{
		FieldMapping:       in.FieldMapping,
		TemplateMapping:    in.TemplateMapping,
		DryRun:             in.DryRun,
		ConfirmCardRemoval: in.ConfirmCardRemoval,
	})
	result.NotesMoved = impact.NotesMoved
	result.CardsMoved = impact.CardsMoved
	result.CardsAdded = impact.CardsAdded
	result.CardsRemoved = impact.CardsRemoved
	result.ReviewsOnRemovedCards = impact.ReviewsOnRemovedCards
	if err != nil {
		if errors.Is(err, store.ErrCardRemovalUnconfirmed) {
			return result, newServiceError(http.StatusConflict, CodeCardRemovalUnconfirmed, "")
		}
		return result, noteTypeChangeError(err)
	}
	if in.DryRun {
		// A dry run writes nothing, and that includes the audit trail: the row would claim a change that never happened.
		return result, nil
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteTypeChange,
		TargetType: "note_type",
		TargetID:   store.Ptr(source.ID),
		Detail: map[string]any{
			"deck_id":                  d.PublicID,
			"source_note_type":         source.PublicID,
			"target_note_type":         target.PublicID,
			"notes_moved":              impact.NotesMoved,
			"cards_moved":              impact.CardsMoved,
			"cards_added":              impact.CardsAdded,
			"cards_removed":            impact.CardsRemoved,
			"reviews_on_removed_cards": impact.ReviewsOnRemovedCards,
		},
	})
	return result, nil
}

// noteTypeChangeError maps a store change error to the service error shape.
func noteTypeChangeError(err error) error {
	var ve *notetype.ValidationError
	if errors.As(err, &ve) {
		return newServiceError(http.StatusBadRequest, CodeInvalidRequest, ve.Detail)
	}
	if errors.Is(err, store.ErrNoteTypeDeckMismatch) || errors.Is(err, gorm.ErrRecordNotFound) {
		return newServiceError(http.StatusNotFound, CodeNotFound, "note type not found")
	}
	if errors.Is(err, store.ErrNoteTypeKindMismatch) || errors.Is(err, store.ErrNoteTypeMappingInvalid) {
		return newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	}
	return newServiceError(http.StatusInternalServerError, CodeInternal, "failed to change the note type")
}

// ---- REST handlers ----

// listEmptyCards is GET /api/v1/decks/:id/empty-cards, scope: read, editor role.
func (a *API) listEmptyCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(c.Request.Context(), publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	cards, err := a.ListEmptyCards(c.Request.Context(), u.ID, d.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, EmptyCardListResponse{Cards: cards, Count: len(cards)})
}

// cleanupEmptyCardsRequest is the cleanup body: dry_run previews the counts, confirm_card_removal allows the
// soft-delete. The switch has the same name as the note type update's, so one convention covers both.
type cleanupEmptyCardsRequest struct {
	DryRun             bool `json:"dry_run"`
	ConfirmCardRemoval bool `json:"confirm_card_removal"`
}

// cleanupEmptyCards is POST /api/v1/decks/:id/empty-cards, scope: write, editor role.
func (a *API) cleanupEmptyCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(c.Request.Context(), publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var req cleanupEmptyCardsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	result, err := a.CleanupEmptyCards(c.Request.Context(), u.ID, d.ID, CurrentAPIKeyID(c), CleanupEmptyCardsInput{
		DryRun:             req.DryRun,
		ConfirmCardRemoval: req.ConfirmCardRemoval,
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Code == CodeCardRemovalUnconfirmed {
			c.JSON(se.Status, emptyCardCleanupUnconfirmedResponse{
				Error: errorBody{
					Code:    se.Code,
					Message: formatErrorMessage(c.Request.Context(), se.Code, se.Message),
				},
				CardsRemoved:          result.CardsRemoved,
				ReviewsOnRemovedCards: result.ReviewsOnRemovedCards,
			})
			return
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// changeNoteTypeRequest is the change body; the mappings are keyed by field id and template id.
type changeNoteTypeRequest struct {
	TargetNoteType     string            `json:"target_note_type"`
	FieldMapping       map[string]string `json:"field_mapping"`
	TemplateMapping    map[string]string `json:"template_mapping"`
	DryRun             bool              `json:"dry_run"`
	ConfirmCardRemoval bool              `json:"confirm_card_removal"`
}

// changeNoteTypeResponse carries the impact counts of a change note type.
type changeNoteTypeResponse struct {
	DryRun                bool `json:"dry_run"`
	NotesMoved            int  `json:"notes_moved"`
	CardsMoved            int  `json:"cards_moved"`
	CardsAdded            int  `json:"cards_added"`
	CardsRemoved          int  `json:"cards_removed"`
	ReviewsOnRemovedCards int  `json:"reviews_on_removed_cards"`
}

// noteTypeChangeUnconfirmedResponse is the 409 body of a change that would remove cards without confirmation.
type noteTypeChangeUnconfirmedResponse struct {
	Error                 errorBody `json:"error"`
	NotesMoved            int       `json:"notes_moved"`
	CardsMoved            int       `json:"cards_moved"`
	CardsAdded            int       `json:"cards_added"`
	CardsRemoved          int       `json:"cards_removed"`
	ReviewsOnRemovedCards int       `json:"reviews_on_removed_cards"`
}

// changeNoteType is POST /api/v1/decks/:id/note-types/:type_id/change, scope: write, editor role.
func (a *API) changeNoteType(c *gin.Context) {
	u, _ := CurrentUser(c)
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	typePublicID, ok := pathPublicID(c, "type_id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(c.Request.Context(), publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var req changeNoteTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	result, err := a.ChangeNoteType(c.Request.Context(), u.ID, d.ID, typePublicID, CurrentAPIKeyID(c), ChangeNoteTypeInput{
		TargetNoteType:     req.TargetNoteType,
		FieldMapping:       req.FieldMapping,
		TemplateMapping:    req.TemplateMapping,
		DryRun:             req.DryRun,
		ConfirmCardRemoval: req.ConfirmCardRemoval,
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Code == CodeCardRemovalUnconfirmed {
			c.JSON(se.Status, noteTypeChangeUnconfirmedResponse{
				Error: errorBody{
					Code:    se.Code,
					Message: formatErrorMessage(c.Request.Context(), se.Code, se.Message),
				},
				NotesMoved:            result.NotesMoved,
				CardsMoved:            result.CardsMoved,
				CardsAdded:            result.CardsAdded,
				CardsRemoved:          result.CardsRemoved,
				ReviewsOnRemovedCards: result.ReviewsOnRemovedCards,
			})
			return
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, changeNoteTypeResponse{
		DryRun:                result.DryRun,
		NotesMoved:            result.NotesMoved,
		CardsMoved:            result.CardsMoved,
		CardsAdded:            result.CardsAdded,
		CardsRemoved:          result.CardsRemoved,
		ReviewsOnRemovedCards: result.ReviewsOnRemovedCards,
	})
}
