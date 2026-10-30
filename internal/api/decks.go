package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/markdown"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// DeckResponse is the external shape of a deck; REST and MCP share the same shape.
type DeckResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// DescriptionMarkdown reports whether Description is Markdown source rather than HTML.
	DescriptionMarkdown bool `json:"description_markdown"`
	// DescriptionHTML is the description after rendering: Markdown converted when DescriptionMarkdown is set, the stored HTML
	// passed through otherwise. It is unsanitised content and clients must show it only inside the sandboxed frame.
	DescriptionHTML string `json:"description_html"`
	// DescriptionText is DescriptionHTML reduced to one line of plain text, the form every list that prints the description as a
	// string shows. It exists so a list never has to strip markup itself and never prints raw tags.
	DescriptionText string `json:"description_text"`
	NewPerDay       int    `json:"new_per_day"`
	ReviewsPerDay   int    `json:"reviews_per_day"`
	PresetID        string `json:"preset_id"`
	// Autoplay is the deck-wide presentation setting the review frame reads: whether a face starts its audio tags
	// without a click.
	Autoplay  bool      `json:"autoplay"`
	CreatedAt time.Time `json:"created_at"`
	// Role is the caller's explicit relation to this deck: owner (their own) or a granted role of editor/reader.
	// Every deck in the list is necessarily one of the two - there is no third source of "visible only by visibility".
	// Clients use it to decide whether to show "delete" or "leave share".
	Role string `json:"role"`
	// Paused reports whether the caller paused this deck for themselves; a paused deck stays out of the default review queue.
	Paused bool `json:"paused"`
}

// deckResponse keeps the old name for existing in-package callers.
type deckResponse = DeckResponse

// ToDeckResponse maps store.Deck to the external shape as seen by userID; shared by REST and MCP.
//
// ID is the deck's own external id. NewPerDay / ReviewsPerDay / PresetID are **the caller's own**
// study settings on this deck (owners read the deck columns, shared members read their own member settings); the preset's numeric primary key becomes an external id here.
func (a *API) ToDeckResponse(ctx context.Context, userID uint64, d store.Deck, role string) DeckResponse {
	out := DeckResponse{
		ID:                  d.PublicID,
		Name:                d.Name,
		Description:         d.Description,
		DescriptionMarkdown: d.DescriptionIsMarkdown(),
		CreatedAt:           d.CreatedAt,
		Role:                role,
		Autoplay:            d.AutoplayEnabled(),
	}
	// The description reaches the client as HTML either way: an HTML deck passes its stored text through, a Markdown deck is
	// rendered here so the server owns the one rendering rule. A render failure falls back to the source text, which the frame
	// still shows as text instead of losing the description.
	out.DescriptionHTML = d.Description
	if out.DescriptionMarkdown {
		rendered, err := markdown.Render(d.Description)
		if err != nil {
			a.logger.Error("render deck description markdown failed", "deck_id", d.ID, "error", err)
		} else {
			out.DescriptionHTML = rendered
		}
	}
	// Lists print the description as a single line of text, so the stripping happens once here rather than in every client.
	out.DescriptionText = notetype.PlainText(out.DescriptionHTML)
	paused, err := a.decks.IsPaused(ctx, userID, d.ID)
	if err != nil {
		a.logger.Error("load deck pause for deck response failed", "deck_id", d.ID, "user_id", userID, "error", err)
	}
	out.Paused = paused
	settings, err := a.decks.StudySettings(ctx, userID, &d)
	if err != nil {
		a.logger.Error("load study settings for deck response failed", "deck_id", d.ID, "user_id", userID, "error", err)
		return out
	}
	out.NewPerDay = settings.Caps.NewPerDay
	out.ReviewsPerDay = settings.Caps.ReviewsPerDay
	if p, err := a.presets.ByID(ctx, settings.PresetID); err == nil {
		out.PresetID = p.PublicID
	}
	return out
}

// toDeckResponse keeps the old name for existing in-package callers.
func (a *API) toDeckResponse(ctx context.Context, userID uint64, d store.Deck, role string) DeckResponse {
	return a.ToDeckResponse(ctx, userID, d, role)
}

// listDecks returns the decks visible to the current user (the logic lives in the service layer's ListDecks, the same source as MCP's list_decks).
func (a *API) listDecks(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	decks, err := a.ListDecks(ctx, u.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	out := make([]deckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, a.toDeckResponse(ctx, u.ID, d.Deck, d.Role))
	}
	c.JSON(http.StatusOK, gin.H{"decks": out})
}

// createDeckRequest is the create-deck request body; when preset_id is absent the Default preset is used (or created) automatically.
// preset_id is the preset's external id (an opaque string); the empty string means the default.
type createDeckRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PresetID    string `json:"preset_id"`
}

// createDeck creates a deck (scope: write): only JSON binding and wrapping here, the logic is in the service layer's CreateDeck
// (REST and the built-in MCP share one implementation and must not duplicate the validation).
func (a *API) createDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	var req createDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	// preset_id is an external id: an empty string lets the service pick the default preset; otherwise it is resolved to a primary key and passed as a number.
	var presetID uint64
	if pid := strings.TrimSpace(req.PresetID); pid != "" {
		p, err := a.presets.ByPublicID(ctx, pid)
		if err != nil {
			abortNotFound(c)
			return
		}
		presetID = p.ID
	}
	d, err := a.CreateDeck(ctx, u, CreateDeckInput{
		Name:        req.Name,
		Description: req.Description,
		PresetID:    presetID,
		APIKeyID:    CurrentAPIKeyID(c),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, a.toDeckResponse(ctx, u.ID, *d, store.RoleOwner))
}

// updateDeckRequest is the request body for changing a deck's name and description (PATCH).
// Pointers distinguish "not provided" from "provided as empty string": a name-only request must not clear the description; only an explicit empty description clears it.
type updateDeckRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// updateDeck changes a deck's name and description (PATCH /api/v1/decks/:id, scope: write, owner only).
// Only JSON binding and wrapping; logic and validation live in the service layer's UpdateDeck (shared by REST and MCP).
func (a *API) updateDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var req updateDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	updated, err := a.UpdateDeck(ctx, u, d.ID, UpdateDeckInput{
		Name:        req.Name,
		Description: req.Description,
		APIKeyID:    CurrentAPIKeyID(c),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// A successful update means the caller is the owner (the service layer gates on that), so the role in the response follows from it.
	c.JSON(http.StatusOK, a.toDeckResponse(ctx, u.ID, *updated, store.RoleOwner))
}

func (a *API) deleteDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	if err := a.DeleteDeck(ctx, u, d.ID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// bulkDecksRequest is the request body of POST /api/v1/decks/bulk. deck_ids are the decks' external ids;
// dry_run counts what would be deleted without writing or auditing.
type bulkDecksRequest struct {
	Action  string   `json:"action"`
	DeckIDs []string `json:"deck_ids"`
	DryRun  bool     `json:"dry_run"`
}

// bulkDeleteDecks applies one bulk action to a set of decks (POST /api/v1/decks/bulk, scope: write).
// Only JSON binding and wrapping here: validation, the per-row owner check and auditing live in the service layer's
// BulkDecks, shared by REST and the built-in MCP tool bulk_decks.
func (a *API) bulkDeleteDecks(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	var req bulkDecksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	resp, err := a.BulkDecks(ctx, u.ID, CurrentAPIKeyID(c), BulkDecksInput{
		Action:  req.Action,
		DeckIDs: req.DeckIDs,
		DryRun:  req.DryRun,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// leaveDeck lets a share recipient leave a deck (DELETE /api/v1/decks/:id/membership, scope: write).
//
// It is an independent path from deleteDeck: leaving removes only your own grant row, while deleting the deck is the owner's irreversible operation.
// The logic is in the service layer's LeaveDeck (REST and MCP share one implementation).
func (a *API) leaveDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	if err := a.LeaveDeck(ctx, u, d.ID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"left": true})
}
