package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// DeckResponse 是卡组的对外形态；REST 与 MCP 共用同一形态。
type DeckResponse struct {
	ID            uint64     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	NewPerDay     int        `json:"new_per_day"`
	ReviewsPerDay int        `json:"reviews_per_day"`
	PresetID      uint64     `json:"preset_id"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	// Role 是调用者在该卡组上的显式关系：owner（自有）或授权角色 editor/reader。
	// 列表里的每张卡组都必然是二者之一——没有第三种「仅因可见性而看得到」的来源。
	// 客户端据此决定显示「删除」还是「退出共享」。
	Role string `json:"role"`
}

// deckResponse 保留旧名，供包内既有调用。
type deckResponse = DeckResponse

// ToDeckResponse 把 store.Deck 映射成对外形态；REST 与 MCP 共用。
// role 是调用者在该卡组上的显式关系（见 DeckResponse.Role），由 service 层解析后传入。
func ToDeckResponse(d store.Deck, role string) DeckResponse {
	return DeckResponse{
		ID:            d.ID,
		Name:          d.Name,
		Description:   d.Description,
		NewPerDay:     d.NewPerDay,
		ReviewsPerDay: d.ReviewsPerDay,
		PresetID:      d.PresetID,
		ArchivedAt:    d.ArchivedAt,
		CreatedAt:     d.CreatedAt,
		Role:          role,
	}
}

// toDeckResponse 保留旧名，供包内既有调用。
func toDeckResponse(d store.Deck, role string) DeckResponse { return ToDeckResponse(d, role) }

// listDecks 返回当前用户可见的卡组（业务逻辑在 service 层 ListDecks，与 MCP 的 list_decks 同源）。
func (a *API) listDecks(c *gin.Context) {
	u, _ := CurrentUser(c)
	decks, err := a.ListDecks(c.Request.Context(), u.ID)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	out := make([]deckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, toDeckResponse(d.Deck, d.Role))
	}
	c.JSON(http.StatusOK, gin.H{"decks": out})
}

// createDeckRequest 是建卡组的请求体；preset_id 缺省时自动使用（或创建）Default 预设。
type createDeckRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PresetID    uint64 `json:"preset_id"`
}

// createDeck 建卡组（scope: write）：只做 JSON 绑定与包壳，业务在 service 层 CreateDeck
// （REST 与内置 MCP 共用同一实现，不得各自复制校验）。
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
		PresetID:    req.PresetID,
		APIKeyID:    CurrentAPIKeyID(c),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toDeckResponse(*d, store.RoleOwner))
}

func (a *API) deleteDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := a.DeleteDeck(c.Request.Context(), u, deckID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// leaveDeck 让被共享者退出卡组（DELETE /api/v1/decks/:id/membership，scope: write）。
//
// 与 deleteDeck 是两条独立路径：退出只撤掉自己一行授权，删除卡组才是 owner 的不可逆操作。
// 业务在 service 层 LeaveDeck（REST 与 MCP 共用同一实现）。
func (a *API) leaveDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	deckID, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := a.LeaveDeck(c.Request.Context(), u, deckID, CurrentAPIKeyID(c)); err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"left": true})
}
