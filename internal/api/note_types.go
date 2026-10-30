package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// NoteTypeJSON is the external shape of a deck's note type.
//
// config is the decoded internal/notetype.Config rather than the config_json column, so a client sees
// the field ids, field names, templates and CSS it needs to build a note payload. The decoded form is
// the same value the store validated on write; nothing is reshaped.
type NoteTypeJSON struct {
	ID        string          `json:"id"`
	DeckID    string          `json:"deck_id"`
	Name      string          `json:"name"`
	Version   int             `json:"version"`
	Config    notetype.Config `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NoteTypeListResponse is the response body of the note type list.
type NoteTypeListResponse struct {
	NoteTypes []NoteTypeJSON `json:"note_types"`
}

// CreateNoteTypeInput is the input for creating a note type; Name and Config are validated by the store.
//
// StockID names one of the stock definitions listed by GET /api/v1/note-type-stocks. When it is
// set, the server builds the config from that definition and Config is ignored, so a client never
// has to carry a copy of a stock config. Name may be empty then and defaults to the stock name.
type CreateNoteTypeInput struct {
	Name    string
	Config  notetype.Config
	StockID string
}

// UpdateNoteTypeInput is the input for updating a note type. Version is the version the caller read;
// it is required, so a caller cannot update blind and silently overwrite a newer edit.
//
// DryRun and ConfirmCardRemoval are the card-impact switches: a template change adds cards, and if it
// removes any, the caller must first see the counts and confirm. The two switches are independent, so
// a caller may also preview a change and then confirm it with one body.
type UpdateNoteTypeInput struct {
	Name               string
	Config             notetype.Config
	Version            *int
	DryRun             bool
	ConfirmCardRemoval bool
}

// UpdateNoteTypeResult is the updated note type together with the card impact of the update.
//
// The counts are reported for a dry run and for a real update alike, and they are also filled on the
// refusal a card removal triggers without confirmation, so the caller can show the same numbers in
// both responses.
type UpdateNoteTypeResult struct {
	NoteType              NoteTypeJSON
	DryRun                bool
	CardsAdded            int
	CardsRemoved          int
	ReviewsOnRemovedCards int
}

// NoteTypeJSON renders one note type, decoding its stored config.
func (a *API) NoteTypeJSON(nt *store.NoteType, deckPublicID string) (NoteTypeJSON, error) {
	cfg, err := nt.Config()
	if err != nil {
		return NoteTypeJSON{}, err
	}
	return NoteTypeJSON{
		ID:        nt.PublicID,
		DeckID:    deckPublicID,
		Name:      nt.Name,
		Version:   nt.Version,
		Config:    cfg,
		CreatedAt: nt.CreatedAt,
		UpdatedAt: nt.UpdatedAt,
	}, nil
}

// noteTypesJSON renders a whole deck's note type list.
func (a *API) noteTypesJSON(types []store.NoteType, deckPublicID string) ([]NoteTypeJSON, error) {
	out := make([]NoteTypeJSON, 0, len(types))
	for i := range types {
		entry, err := a.NoteTypeJSON(&types[i], deckPublicID)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// ListNoteTypes lists one deck's note types (scope: read, reader role). The order is the store's:
// name then id, so two identically named types keep a stable order.
func (a *API) ListNoteTypes(ctx context.Context, userID, deckID uint64) ([]NoteTypeJSON, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader)
	if err != nil {
		return nil, err
	}
	types, err := a.noteTypes.ListByDeck(ctx, d.ID)
	if err != nil {
		a.logger.Error("list note types failed", "deck_id", d.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list note types")
	}
	out, err := a.noteTypesJSON(types, d.PublicID)
	if err != nil {
		a.logger.Error("decode note type config failed", "deck_id", d.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read note type")
	}
	return out, nil
}

// GetNoteType reads one note type of a deck (scope: read, reader role). A type of another deck is
// not found, never a forbidden resource, so the response does not reveal note types outside the deck.
func (a *API) GetNoteType(ctx context.Context, userID, deckID uint64, publicID string) (NoteTypeJSON, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader)
	if err != nil {
		return NoteTypeJSON{}, err
	}
	nt, err := a.noteTypeInDeck(ctx, d.ID, publicID)
	if err != nil {
		return NoteTypeJSON{}, err
	}
	out, err := a.NoteTypeJSON(nt, d.PublicID)
	if err != nil {
		a.logger.Error("decode note type config failed", "note_type_id", nt.ID, "error", err)
		return NoteTypeJSON{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read note type")
	}
	return out, nil
}

// CreateNoteType writes a note type into a deck (scope: write, editor role). Name and config are
// validated before the row is written; a validation error is 400 and names the offending field or template.
func (a *API) CreateNoteType(ctx context.Context, userID, deckID uint64, apiKeyID *uint64, in CreateNoteTypeInput) (NoteTypeJSON, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return NoteTypeJSON{}, err
	}
	name := in.Name
	cfg := in.Config
	if in.StockID != "" {
		def, ok := stockNoteTypeByID(in.StockID)
		if !ok {
			return NoteTypeJSON{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "unknown stock note type \""+in.StockID+"\"")
		}
		cfg = def.Config
		name = stockNoteTypeName(name, def.JSON.Name)
	}
	nt := store.NoteType{DeckID: d.ID, Name: name}
	if err := a.noteTypes.Create(ctx, &nt, cfg); err != nil {
		return NoteTypeJSON{}, noteTypeWriteError(err)
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteTypeCreate,
		TargetType: "note_type",
		TargetID:   store.Ptr(nt.ID),
		Detail:     map[string]any{"deck_id": d.PublicID, "name": nt.Name},
	})
	out, err := a.NoteTypeJSON(&nt, d.PublicID)
	if err != nil {
		return NoteTypeJSON{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read note type")
	}
	return out, nil
}

// UpdateNoteType replaces a note type's name and config when the caller's version is still current
// (scope: write, editor role). A moved version is 409 version_conflict; validation is 400 and names
// the offending field or template.
//
// The kind is fixed at creation: a normal type cannot become cloze or the reverse, because the two
// kinds generate and render cards by different rules, so an existing note's cards could not be carried
// over. Such an update is 400 invalid_request and writes nothing.
//
// Cards follow the config. Every live note of the type is regenerated from the new config in the
// same transaction as the row, so an added template yields a card per note whose front is non-empty,
// and a removed template soft-deletes the cards it no longer generates while their card_states and
// reviews rows stay. A dry run returns the counts and writes nothing; an update that would remove
// cards without ConfirmCardRemoval is refused with 409 card_removal_unconfirmed and the same counts.
// A successful update writes one audit row recording the counts.
func (a *API) UpdateNoteType(ctx context.Context, userID, deckID uint64, publicID string, apiKeyID *uint64, in UpdateNoteTypeInput) (UpdateNoteTypeResult, error) {
	empty := UpdateNoteTypeResult{DryRun: in.DryRun}
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return empty, err
	}
	if in.Version == nil {
		return empty, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "version is required")
	}
	nt, err := a.noteTypeInDeck(ctx, d.ID, publicID)
	if err != nil {
		return empty, err
	}
	currentCfg, err := nt.Config()
	if err != nil {
		a.logger.Error("decode note type config failed", "note_type_id", nt.ID, "error", err)
		return empty, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read note type")
	}
	if currentCfg.Kind != in.Config.Kind {
		return empty, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "the note type kind cannot change")
	}
	// Keep the stored row as a value so a dry run can render what is still in the database; nt itself
	// is mutated by the store's validation before the transaction decides whether to commit.
	before := *nt
	nt.Name = in.Name
	impact, err := a.noteTypes.Update(ctx, nt, in.Config, store.NoteTypeUpdateOptions{
		ExpectedVersion:    *in.Version,
		DryRun:             in.DryRun,
		ConfirmCardRemoval: in.ConfirmCardRemoval,
	})
	result := UpdateNoteTypeResult{
		DryRun:                in.DryRun,
		CardsAdded:            impact.CardsAdded,
		CardsRemoved:          impact.CardsRemoved,
		ReviewsOnRemovedCards: impact.ReviewsOnRemovedCards,
	}
	if err != nil {
		if errors.Is(err, store.ErrCardRemovalUnconfirmed) {
			return result, newServiceError(http.StatusConflict, CodeCardRemovalUnconfirmed, "")
		}
		return result, noteTypeWriteError(err)
	}
	source := nt
	if in.DryRun {
		source = &before
	}
	out, err := a.NoteTypeJSON(source, d.PublicID)
	if err != nil {
		return result, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read note type")
	}
	result.NoteType = out
	if in.DryRun {
		// A dry run writes nothing, and that includes the audit trail: the row would claim a change
		// that never happened.
		return result, nil
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteTypeUpdate,
		TargetType: "note_type",
		TargetID:   store.Ptr(nt.ID),
		Detail: map[string]any{
			"deck_id":                  d.PublicID,
			"name":                     nt.Name,
			"version":                  nt.Version,
			"cards_added":              impact.CardsAdded,
			"cards_removed":            impact.CardsRemoved,
			"reviews_on_removed_cards": impact.ReviewsOnRemovedCards,
		},
	})
	return result, nil
}

// DeleteNoteType removes a note type (scope: write, editor role). It is refused with 409
// note_type_in_use while a note still references it, because deleting then would orphan the note's content.
func (a *API) DeleteNoteType(ctx context.Context, userID, deckID uint64, publicID string, apiKeyID *uint64) error {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return err
	}
	nt, err := a.noteTypeInDeck(ctx, d.ID, publicID)
	if err != nil {
		return err
	}
	if err := a.noteTypes.Delete(ctx, nt.ID); err != nil {
		if errors.Is(err, store.ErrNoteTypeInUse) {
			return newServiceError(http.StatusConflict, CodeNoteTypeInUse, err.Error())
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return newServiceError(http.StatusNotFound, CodeNotFound, "note type not found")
		}
		a.logger.Error("delete note type failed", "note_type_id", nt.ID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "failed to delete note type")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteTypeDelete,
		TargetType: "note_type",
		TargetID:   store.Ptr(nt.ID),
		Detail:     map[string]any{"deck_id": d.PublicID, "name": nt.Name},
	})
	return nil
}

// noteTypeInDeck resolves a note type by public id and requires it to belong to deckID. A type of
// another deck is reported as not found, so a deck's endpoint never confirms ids outside it.
func (a *API) noteTypeInDeck(ctx context.Context, deckID uint64, publicID string) (*store.NoteType, error) {
	nt, err := a.noteTypes.ByPublicID(ctx, publicID)
	if err != nil || nt.DeckID != deckID {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "note type not found")
	}
	return nt, nil
}

// noteTypeWriteError maps a store write error to the service error shape.
func noteTypeWriteError(err error) error {
	var ve *notetype.ValidationError
	if errors.As(err, &ve) {
		return newServiceError(http.StatusBadRequest, CodeInvalidRequest, ve.Detail)
	}
	if errors.Is(err, store.ErrVersionConflict) {
		return newServiceError(http.StatusConflict, CodeVersionConflict, "the note type was changed by someone else; reload and try again")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return newServiceError(http.StatusNotFound, CodeNotFound, "note type not found")
	}
	return newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
}

// ---- REST handlers ----

// listNoteTypes is GET /api/v1/decks/:id/note-types, scope: read.
func (a *API) listNoteTypes(c *gin.Context) {
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
	types, err := a.ListNoteTypes(c.Request.Context(), u.ID, d.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, NoteTypeListResponse{NoteTypes: types})
}

// getNoteType is GET /api/v1/decks/:id/note-types/:type_id, scope: read.
func (a *API) getNoteType(c *gin.Context) {
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
	nt, err := a.GetNoteType(c.Request.Context(), u.ID, d.ID, typePublicID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, nt)
}

// createNoteTypeRequest is the create body: name plus either a stock definition id or the
// config value in the same shape GET returns. stock_id and config are alternatives; stock_id
// wins when both are present.
type createNoteTypeRequest struct {
	Name    string          `json:"name"`
	Config  notetype.Config `json:"config"`
	StockID string          `json:"stock_id"`
}

// createNoteType is POST /api/v1/decks/:id/note-types, scope: write, editor role.
func (a *API) createNoteType(c *gin.Context) {
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
	var req createNoteTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	nt, err := a.CreateNoteType(c.Request.Context(), u.ID, d.ID, CurrentAPIKeyID(c), CreateNoteTypeInput{
		Name:    req.Name,
		Config:  req.Config,
		StockID: req.StockID,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, nt)
}

// updateNoteTypeRequest is the update body; version is required and must be the value the caller read.
// dry_run previews the card impact, and confirm_card_removal allows the update to soft-delete cards.
type updateNoteTypeRequest struct {
	Name               string          `json:"name"`
	Config             notetype.Config `json:"config"`
	Version            *int            `json:"version"`
	DryRun             bool            `json:"dry_run"`
	ConfirmCardRemoval bool            `json:"confirm_card_removal"`
}

// updateNoteTypeResponse is the note type plus the card impact. It embeds NoteTypeJSON, so a client
// that read the old response keeps finding id, name, config and version at the same locations.
type updateNoteTypeResponse struct {
	NoteTypeJSON
	DryRun                bool `json:"dry_run"`
	CardsAdded            int  `json:"cards_added"`
	CardsRemoved          int  `json:"cards_removed"`
	ReviewsOnRemovedCards int  `json:"reviews_on_removed_cards"`
}

// cardRemovalUnconfirmedResponse carries the standard error envelope plus the impact counts, so the
// caller sees exactly what confirming would remove.
type cardRemovalUnconfirmedResponse struct {
	Error                 errorBody `json:"error"`
	CardsAdded            int       `json:"cards_added"`
	CardsRemoved          int       `json:"cards_removed"`
	ReviewsOnRemovedCards int       `json:"reviews_on_removed_cards"`
}

// updateNoteType is PATCH /api/v1/decks/:id/note-types/:type_id, scope: write, editor role.
func (a *API) updateNoteType(c *gin.Context) {
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
	var req updateNoteTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	result, err := a.UpdateNoteType(c.Request.Context(), u.ID, d.ID, typePublicID, CurrentAPIKeyID(c), UpdateNoteTypeInput{
		Name:               req.Name,
		Config:             req.Config,
		Version:            req.Version,
		DryRun:             req.DryRun,
		ConfirmCardRemoval: req.ConfirmCardRemoval,
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Code == CodeCardRemovalUnconfirmed {
			c.JSON(se.Status, cardRemovalUnconfirmedResponse{
				Error: errorBody{
					Code:    se.Code,
					Message: formatErrorMessage(c.Request.Context(), se.Code, se.Message),
				},
				CardsAdded:            result.CardsAdded,
				CardsRemoved:          result.CardsRemoved,
				ReviewsOnRemovedCards: result.ReviewsOnRemovedCards,
			})
			return
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, updateNoteTypeResponse{
		NoteTypeJSON:          result.NoteType,
		DryRun:                result.DryRun,
		CardsAdded:            result.CardsAdded,
		CardsRemoved:          result.CardsRemoved,
		ReviewsOnRemovedCards: result.ReviewsOnRemovedCards,
	})
}

// deleteNoteType is DELETE /api/v1/decks/:id/note-types/:type_id, scope: write, editor role.
func (a *API) deleteNoteType(c *gin.Context) {
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
	if err := a.DeleteNoteType(c.Request.Context(), u.ID, d.ID, typePublicID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": typePublicID})
}
