package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

// This file resolves external ids (opaque strings) to numeric primary keys, for callers other than REST to reuse.
//
// The convention is "resolve in the transport layer, keep numbers in the business layer": a built-in MCP tool turns public ids into primary keys here first,
// then calls exactly the same service methods as REST, so REST and MCP do not each grow their own resolution.

// DeckByPublicID resolves a deck by external id; unknown, empty or malformed always yields 404 not_found.
func (a *API) DeckByPublicID(ctx context.Context, publicID string) (*store.Deck, error) {
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return d, nil
}

// NoteByPublicID resolves a note by external id; unknown, empty or soft-deleted always yields 404 not_found.
func (a *API) NoteByPublicID(ctx context.Context, publicID string) (*store.Note, error) {
	n, err := a.notes.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return n, nil
}

// PresetByPublicID resolves a preset by external id; unknown or empty yields 404 not_found.
func (a *API) PresetByPublicID(ctx context.Context, publicID string) (*store.Preset, error) {
	p, err := a.presets.ByPublicID(ctx, publicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	return p, nil
}

// ImportPresetID resolves an optional owned preset after trimming whitespace, so import
// transports agree on selection and database failures remain distinguishable from invalid input.
func (a *API) ImportPresetID(ctx context.Context, owner uint64, publicID string) (uint64, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return 0, nil
	}
	p, err := a.presets.ByPublicID(ctx, publicID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, InvalidRequest("invalid preset selection")
	}
	if err != nil {
		return 0, fmt.Errorf("resolve import preset: %w", err)
	}
	if p.OwnerUserID != owner {
		return 0, InvalidRequest("invalid preset selection")
	}
	return p.ID, nil
}
