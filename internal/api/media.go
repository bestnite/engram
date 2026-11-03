package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// MediaItem is the external shape of one entry in the media library list.
type MediaItem struct {
	Sha256    string    `json:"sha256"`
	Mime      string    `json:"mime"`
	Bytes     int64     `json:"bytes"`
	URL       string    `json:"url"`
	Width     *int      `json:"width,omitempty"`
	Height    *int      `json:"height,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Names are the deck-scoped file names visible decks registered for these bytes, sorted and deduplicated. A name
	// from a deck the caller cannot see never appears here.
	Names []string `json:"names"`
	// Decks are the visible decks that reference the medium by a live note or hold a name for it, sorted by name.
	Decks []MediaDeck `json:"decks"`
	// State is the reclamation state, one of referenced, pending, restorable or unreferenced. It is computed from the
	// same definition the media collector reclaims by.
	State string `json:"state"`
	// CleanupAt is when a sweep may remove the bytes: null unless State is pending, where it is the orphan stamp plus
	// the server's retention grace.
	CleanupAt *time.Time `json:"cleanup_at"`
}

// MediaDeck is one visible deck a medium belongs to, by its external id.
type MediaDeck struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MediaPage is the paged response: items is this page, next_cursor is the cursor for the next page (empty string = end).
type MediaPage struct {
	Items      []MediaItem `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

// MediaFilter narrows a media library page; a zero value means 'no filter'.
//
// DeckID is a numeric deck primary key that the transport has already resolved from the caller's `deck` public id and
// found readable; the listing itself does not re-check deck visibility.
type MediaFilter struct {
	DeckID uint64
	Class  string
	State  string
	Query  string
}

// ListReadableMedia returns one page of media the user can read (REST `GET /api/v1/media`).
//
// The business rules live in the store (readability predicate, reference state and keyset paging); this only does
// parameter validation, error mapping and shape conversion:
//   - limit must fall in [1, MaxMediaPageSize], otherwise 400 (never silently clamp to the maximum - the caller would think
//     it got the requested number of rows);
//   - an unknown class or state is 400 with the same stable code, instead of silently returning everything or nothing;
//   - a cursor that cannot be decoded is likewise 400 (silently restarting would make paging loop quietly);
//   - on an empty page Items is an empty slice rather than null, keeping the JSON shape stable.
func (a *API) ListReadableMedia(ctx context.Context, userID uint64, limit int, cursor string, filter MediaFilter) (MediaPage, error) {
	if limit < 1 || limit > store.MaxMediaPageSize {
		return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("limit must be between 1 and %d", store.MaxMediaPageSize))
	}
	if filter.Class != "" && !store.IsMediaClass(filter.Class) {
		return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "class is invalid")
	}
	if filter.State != "" && !store.IsMediaState(filter.State) {
		return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "state is invalid")
	}
	rows, next, err := store.ListMediaLibrary(ctx, a.db, userID, limit, cursor, store.MediaLibraryFilter{
		DeckID: filter.DeckID,
		Class:  filter.Class,
		State:  filter.State,
		Query:  filter.Query,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidMediaCursor),
			errors.Is(err, store.ErrInvalidMediaClass),
			errors.Is(err, store.ErrInvalidMediaState):
			return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "invalid media list parameter")
		}
		a.logger.Error("list readable media failed", "user_id", userID, "error", err)
		return MediaPage{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list media")
	}
	items := make([]MediaItem, 0, len(rows))
	for _, row := range rows {
		decks := make([]MediaDeck, 0, len(row.Decks))
		for _, deck := range row.Decks {
			decks = append(decks, MediaDeck{ID: deck.PublicID, Name: deck.Name})
		}
		items = append(items, MediaItem{
			Sha256: row.Sha256,
			Mime:   row.Mime,
			Bytes:  row.Bytes,
			// The URL is built from the hash and has the same shape as the url in the upload response: the media picker references it directly
			// instead of re-composing the /media/<sha256> contract on the client.
			URL:       "/media/" + row.Sha256,
			Width:     row.Width,
			Height:    row.Height,
			CreatedAt: row.CreatedAt,
			Names:     row.Names,
			Decks:     decks,
			State:     row.State,
			CleanupAt: row.CleanupAt,
		})
	}
	return MediaPage{Items: items, NextCursor: next}, nil
}

// listMedia is REST `GET /api/v1/media`: scope read, paging and filters validated and handed to the service.
//
// The optional `deck` parameter is a public deck id: it is resolved here and checked with the same read-access rule every
// deck route uses, so a caller cannot narrow by a deck they cannot read. A missing deck is 404 and an unreadable one is
// 403, exactly like the other deck-scoped endpoints.
func (a *API) listMedia(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	limit := store.DefaultMediaPageSize
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("limit must be an integer between 1 and %d", store.MaxMediaPageSize))
			return
		}
		limit = v
	}
	filter := MediaFilter{
		Class: strings.TrimSpace(c.Query("class")),
		State: strings.TrimSpace(c.Query("state")),
		Query: strings.TrimSpace(c.Query("q")),
	}
	if publicID := strings.TrimSpace(c.Query("deck")); publicID != "" {
		deck, err := a.decks.ByPublicID(ctx, publicID)
		if err != nil {
			abortNotFound(c)
			return
		}
		if _, err := a.RequireDeckRole(ctx, u.ID, deck.ID, store.RoleReader); err != nil {
			writeServiceError(c, err)
			return
		}
		filter.DeckID = deck.ID
	}
	page, err := a.ListReadableMedia(ctx, u.ID, limit, strings.TrimSpace(c.Query("cursor")), filter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
