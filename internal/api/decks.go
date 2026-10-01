package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

// DeckResponse 是卡组的对外形态（DESIGN.md §2.2）；REST 与 MCP 共用同一形态。
type DeckResponse struct {
	ID            uint64     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Visibility    string     `json:"visibility"`
	NewPerDay     int        `json:"new_per_day"`
	ReviewsPerDay int        `json:"reviews_per_day"`
	PresetID      uint64     `json:"preset_id"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// deckResponse 保留旧名，供包内既有调用。
type deckResponse = DeckResponse

// ToDeckResponse 把 store.Deck 映射成对外形态；REST 与 MCP 共用。
func ToDeckResponse(d store.Deck) DeckResponse {
	return DeckResponse{
		ID:            d.ID,
		Name:          d.Name,
		Description:   d.Description,
		Visibility:    d.Visibility,
		NewPerDay:     d.NewPerDay,
		ReviewsPerDay: d.ReviewsPerDay,
		PresetID:      d.PresetID,
		ArchivedAt:    d.ArchivedAt,
		CreatedAt:     d.CreatedAt,
	}
}

// toDeckResponse 保留旧名，供包内既有调用。
func toDeckResponse(d store.Deck) DeckResponse { return ToDeckResponse(d) }

// listDecks 返回当前用户拥有的卡组（业务逻辑在 service 层 ListDecks，与 MCP 的 list_decks 同源）。
func (a *API) listDecks(c *gin.Context) {
	u, _ := CurrentUser(c)
	decks, err := a.ListDecks(c.Request.Context(), u.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	out := make([]deckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, toDeckResponse(d))
	}
	c.JSON(http.StatusOK, gin.H{"decks": out})
}

// createDeckRequest 是建卡组的请求体；preset_id 缺省时自动使用（或创建）Default 预设。
type createDeckRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	PresetID    uint64 `json:"preset_id"`
}

// createDeck 建卡组（scope: write）。
func (a *API) createDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	var req createDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	visibility := strings.TrimSpace(req.Visibility)
	if visibility == "" {
		visibility = "private"
	}
	presetID := req.PresetID
	if presetID == 0 {
		id, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			a.logger.Error("ensure default preset failed", "user_id", u.ID, "error", err)
			abortError(c, http.StatusInternalServerError, CodeInternal, "")
			return
		}
		presetID = id
	}
	d := store.Deck{
		OwnerUserID: u.ID,
		Name:        req.Name,
		Description: req.Description,
		Visibility:  visibility,
		PresetID:    presetID,
		CreatedAt:   a.now(),
	}
	if err := a.decks.Create(ctx, &d); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   CurrentAPIKeyID(c),
		Action:     "deck.create",
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail:     map[string]any{"name": d.Name, "visibility": d.Visibility},
	})
	c.JSON(http.StatusCreated, toDeckResponse(d))
}
