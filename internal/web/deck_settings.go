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
// 读写走 /api/v1/decks/:id/settings 的 JSON 端点（deck_settings_api.go）。
func (s *Server) registerDeckSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/decks/:id/settings", s.deckSettingsRoute)
}

// deckBudget 取单个卡组今日的额度情况，走 schedule.DeckBudgets（与复习队列同源）。
func (s *Server) deckBudget(ctx context.Context, user *store.User, deckID uint64) (schedule.DeckBudget, error) {
	budgets, err := schedule.NewQueueBuilder(s.db, s.decks, nil).DeckBudgets(ctx, user.ID, []uint64{deckID}, schedule.QueueOptions{
		Timezone:      user.Timezone,
		DayCutoffHour: user.DayCutoffHour,
	})
	if err != nil {
		return schedule.DeckBudget{}, err
	}
	return budgets[deckID], nil
}

// deckSettingsRoute 提供 GET /decks/:id/settings：只发 SPA 应用壳，
// 由客户端路由渲染卡组每日上限页；读写走 /api/v1/decks/:id/settings（同一份服务逻辑与审计）。
//
// 门禁留在服务端：页面编辑的是调用者自己在该卡组上的学习设置，卡组的任何成员（reader 及以上）
// 都可以打开；无权访问与不存在的卡组在返回应用壳之前就以 403 / 404 结束。
func (s *Server) deckSettingsRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
		return
	}
	s.shell.ServeIndex(c)
}
