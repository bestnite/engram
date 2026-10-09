package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// DeckResponse 是卡组的对外形态；REST 与 MCP 共用同一形态。
type DeckResponse struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	NewPerDay     int        `json:"new_per_day"`
	ReviewsPerDay int        `json:"reviews_per_day"`
	PresetID      string     `json:"preset_id"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	// Role 是调用者在该卡组上的显式关系：owner（自有）或授权角色 editor/reader。
	// 列表里的每张卡组都必然是二者之一——没有第三种「仅因可见性而看得到」的来源。
	// 客户端据此决定显示「删除」还是「退出共享」。
	Role string `json:"role"`
}

// deckResponse 保留旧名，供包内既有调用。
type deckResponse = DeckResponse

// ToDeckResponse 把 store.Deck 映射成 userID 视角下的对外形态；REST 与 MCP 共用。
//
// ID 取卡组自己的对外 id。NewPerDay / ReviewsPerDay / PresetID 是**调用者自己**在该卡组上的
// 学习设置（属主读卡组列，共享成员读自己的成员设置），预设的数字主键在这里换成对外 id。
func (a *API) ToDeckResponse(ctx context.Context, userID uint64, d store.Deck, role string) DeckResponse {
	out := DeckResponse{
		ID:          d.PublicID,
		Name:        d.Name,
		Description: d.Description,
		ArchivedAt:  d.ArchivedAt,
		CreatedAt:   d.CreatedAt,
		Role:        role,
	}
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

// toDeckResponse 保留旧名，供包内既有调用。
func (a *API) toDeckResponse(ctx context.Context, userID uint64, d store.Deck, role string) DeckResponse {
	return a.ToDeckResponse(ctx, userID, d, role)
}

// listDecks 返回当前用户可见的卡组（业务逻辑在 service 层 ListDecks，与 MCP 的 list_decks 同源）。
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

// createDeckRequest 是建卡组的请求体；preset_id 缺省时自动使用（或创建）Default 预设。
// preset_id 是预设的对外 id（不透明字符串），空串表示缺省。
type createDeckRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PresetID    string `json:"preset_id"`
}

// createDeck 建卡组（scope: write）：只做 JSON 绑定与包壳，业务在 service 层 CreateDeck
// （REST 与内置 MCP 共用同一实现，不得各自复制校验）。
func (a *API) createDeck(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	var req createDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	// preset_id 是对外 id：空串交给 service 取缺省预设；非空则先解析成主键再传数字。
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

// updateDeckRequest 是修改卡组名称与描述的请求体（PATCH）。
// 用指针区分「未提供」与「提供了空串」：只带名称的请求不该把描述清空；显式给空描述才清空。
type updateDeckRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// updateDeck 修改卡组的名称与描述（PATCH /api/v1/decks/:id，scope: write，仅 owner）。
// 只做 JSON 绑定与包壳，业务与校验在 service 层 UpdateDeck（REST 与 MCP 共用）。
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
	// 修改成功即调用者是 owner（service 层以此为条件），响应里的角色据此给出。
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

// leaveDeck 让被共享者退出卡组（DELETE /api/v1/decks/:id/membership，scope: write）。
//
// 与 deleteDeck 是两条独立路径：退出只撤掉自己一行授权，删除卡组才是 owner 的不可逆操作。
// 业务在 service 层 LeaveDeck（REST 与 MCP 共用同一实现）。
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
