package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

func (s *Server) registerDeckQueueCountRoute(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil {
		return
	}
	// SPA 卡组计数只接受当前会话，避免将 session-only 的可见范围暴露给 API Key。
	router.GET("/api/v1/decks/queue-counts", s.deckQueueCountsAPI)
}

// registerDeckRoutes 挂载卡组列表页。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerDeckRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	// GET /decks 已切到 SPA 规范路径：deckListRoute 返回应用壳，由客户端路由渲染卡组列表，
	// 数据走既有 JSON 端点。
	router.GET("/decks", s.deckListRoute)
}

// deckListRoute 提供 GET /decks：返回应用壳，由客户端路由渲染卡组列表，
// 数据走 GET /api/v1/decks 与 GET /api/v1/decks/queue-counts。
//
// 与迁移前的 SSR 列表页一样先要求已登录会话：匿名一律重定向登录页，页面迁移不改动授权判定，
// 也不新增写路径。
func (s *Server) deckListRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

type deckQueueCountsResponse struct {
	Decks []deckQueueCount `json:"decks"`
}

type deckQueueCount struct {
	DeckID      uint64 `json:"deck_id"`
	NewCount    int    `json:"new_count"`
	ReviewCount int    `json:"review_count"`
}

// deckQueueCountsAPI 返回当前会话可见卡组的队列数，直接复用共享队列构建器。
func (s *Server) deckQueueCountsAPI(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if _, ok := auth.CurrentSession(c); !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	ctx := c.Request.Context()
	summaries, err := s.decks.SummariesVisible(ctx, user.ID)
	if err != nil {
		s.logger.Error("list decks for queue counts failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	counts, err := s.deckQueueCounts(ctx, user.ID, summaries)
	if err != nil {
		s.logger.Error("count deck queue failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	response := deckQueueCountsResponse{Decks: make([]deckQueueCount, 0, len(summaries))}
	for _, summary := range summaries {
		count := counts[summary.Deck.ID]
		response.Decks = append(response.Decks, deckQueueCount{
			DeckID: summary.Deck.ID, NewCount: count.New, ReviewCount: count.Review,
		})
	}
	c.JSON(http.StatusOK, response)
}

// deckQueueCounts 取列表页每个卡组「今日可刷」的构成（新 / 复习两个数），走 schedule.DeckCounts：
// 它与 /review 的取卡路径同源，因此两个数相加＝点进去实际能刷的张数。
// sched 用默认预设（schedulerFor 传 nil）——数量统计不算 retrievability，预设不影响结果。
func (s *Server) deckQueueCounts(ctx context.Context, userID uint64, summaries []store.DeckSummary) (map[uint64]schedule.DeckQueueCounts, error) {
	ids := make([]uint64, 0, len(summaries))
	for i := range summaries {
		ids = append(ids, summaries[i].Deck.ID)
	}
	sched, err := s.schedulerFor(ctx, userID, nil)
	if err != nil {
		return nil, err
	}
	return schedule.NewQueueBuilder(s.db, s.decks, sched).DeckCounts(ctx, userID, ids)
}

// resolvePresetID 解析表单里的 preset_id：必须属于当前用户；缺省或非法时退回第一个预设。
// 预设由 store.EnsureDefaultPreset 保证至少有一个，因此不会出现「无预设可退回」的分支。
func (s *Server) resolvePresetID(ctx context.Context, userID uint64, raw string) (uint64, error) {
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		return 0, err
	}
	want, _ := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	for i := range presets {
		if presets[i].ID == want {
			return presets[i].ID, nil
		}
	}
	return presets[0].ID, nil
}
