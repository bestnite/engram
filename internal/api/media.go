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
}

// MediaPage is the paged response: items is this page, next_cursor is the cursor for the next page (empty string = end).
type MediaPage struct {
	Items      []MediaItem `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

// ListReadableMedia returns one page of media the user can read (REST `GET /api/v1/media`).
//
// The business rules live in the store (readability predicate and keyset paging); this only does parameter validation, error mapping and shape conversion:
//   - limit must fall in [1, MaxMediaPageSize], otherwise 400 (never silently clamp to the maximum - the caller would think
//     it got the requested number of rows);
//   - a cursor that cannot be decoded is likewise 400 (silently restarting would make paging loop quietly);
//   - on an empty page Items is an empty slice rather than null, keeping the JSON shape stable.
func (a *API) ListReadableMedia(ctx context.Context, userID uint64, limit int, cursor string) (MediaPage, error) {
	if limit < 1 || limit > store.MaxMediaPageSize {
		return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("limit must be between 1 and %d", store.MaxMediaPageSize))
	}
	rows, next, err := store.ListReadableMedia(ctx, a.db, userID, limit, cursor)
	if err != nil {
		if errors.Is(err, store.ErrInvalidMediaCursor) {
			return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "cursor is invalid")
		}
		a.logger.Error("list readable media failed", "user_id", userID, "error", err)
		return MediaPage{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list media")
	}
	items := make([]MediaItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, MediaItem{
			Sha256: m.Sha256,
			Mime:   m.Mime,
			Bytes:  m.Bytes,
			// The URL is built from the hash and has the same shape as the url in the upload response: the media picker references it directly
			// instead of re-composing the /media/<sha256> contract on the client.
			URL:       "/media/" + m.Sha256,
			Width:     m.Width,
			Height:    m.Height,
			CreatedAt: m.CreatedAt,
		})
	}
	return MediaPage{Items: items, NextCursor: next}, nil
}

// listMedia is REST `GET /api/v1/media`: scope read, paging parameters validated and handed to the service.
func (a *API) listMedia(c *gin.Context) {
	u, _ := CurrentUser(c)
	limit := store.DefaultMediaPageSize
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("limit must be an integer between 1 and %d", store.MaxMediaPageSize))
			return
		}
		limit = v
	}
	page, err := a.ListReadableMedia(c.Request.Context(), u.ID, limit, strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
