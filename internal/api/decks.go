package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
)

// deckResponse 是卡组的对外形态（DESIGN.md §2.2）。
type deckResponse struct {
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

func toDeckResponse(d store.Deck) deckResponse {
	return deckResponse{
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

// listDecks 返回当前用户拥有的卡组（按权限过滤；M5 会把授权卡组一并纳入）。
func (a *API) listDecks(c *gin.Context) {
	u, _ := CurrentUser(c)
	decks, err := a.decks.ListByOwner(c.Request.Context(), u.ID)
	if err != nil {
		a.logger.Error("list decks failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to list decks")
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
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "request body must be valid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "name is required")
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
			abortError(c, http.StatusInternalServerError, CodeInternal, "failed to prepare default preset")
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
