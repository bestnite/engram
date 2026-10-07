package web

import (
	"context"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// registerDeckSettingsRoutes 挂载卡组设置页（仅 owner）：改每日新卡 / 复习上限。
//
// 门控与 registerDeckRoutes 相同：列表页上渲染的 /decks/:id/settings 链接与这里注册的路由
// 必须共享同一依赖前提，否则会出现「链接在、点进去 404」。SSR 页面层已删除：页面只发应用壳，
// 读写走 /api/v1/decks/:id/settings 的 JSON 端点（spa_deck_settings.go）。
func (s *Server) registerDeckSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/decks/:id/settings", s.deckSettingsRoute)
}

// deckBudget 取单个卡组今日的额度情况，走 schedule.DeckBudgets（与复习队列同源）。
func (s *Server) deckBudget(ctx context.Context, userID, deckID uint64) (schedule.DeckBudget, error) {
	sched, err := s.schedulerFor(ctx, userID, []uint64{deckID})
	if err != nil {
		return schedule.DeckBudget{}, err
	}
	budgets, err := schedule.NewQueueBuilder(s.db, s.decks, sched).DeckBudgets(ctx, userID, []uint64{deckID})
	if err != nil {
		return schedule.DeckBudget{}, err
	}
	return budgets[deckID], nil
}

// deckSettingsRoute 提供 GET /decks/:id/settings：只发 SPA 应用壳，
// 由客户端路由渲染卡组每日上限页；读写走 /api/v1/decks/:id/settings（同一份服务逻辑与审计）。
//
// owner 门禁留在服务端：非 owner 与不存在的卡组在返回应用壳之前就以 403 / 404 结束，
// 与迁移前的 SSR 页面完全一致（否则会让无权用户拿到页面外壳）。SSR 页面层已删除，不再回退。
func (s *Server) deckSettingsRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner); !ok {
		return
	}
	s.spa.ServeIndex(c)
}
