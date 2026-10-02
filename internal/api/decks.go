package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
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

// createDeck 建卡组（scope: write）：只做 JSON 绑定与包壳，业务在 service 层 CreateDeck
// （DESIGN.md §7.4：REST 与内置 MCP 共用同一实现，不得各自复制校验）。
func (a *API) createDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	var req createDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	d, err := a.CreateDeck(c.Request.Context(), u, CreateDeckInput{
		Name:        req.Name,
		Description: req.Description,
		Visibility:  req.Visibility,
		PresetID:    req.PresetID,
		APIKeyID:    CurrentAPIKeyID(c),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toDeckResponse(*d))
}
