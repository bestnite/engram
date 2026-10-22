package api

import (
	"context"
	"net/http"

	"git.nite07.com/nite/engram/internal/store"
)

// This file resolves external ids (opaque strings) to numeric primary keys, for callers other than REST to reuse.
//
// The convention is "resolve in the transport layer, keep numbers in the business layer": a built-in MCP tool turns public ids into primary keys here first,
// then calls exactly the same service methods as REST, so REST and MCP do not each grow their own resolution. Failures are always
// mapped to the same 404 not_found as REST (an empty string also counts as not found, so it never matches some row).

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
