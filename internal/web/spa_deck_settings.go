package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// spaDeckSettingsPayload 把卡组与今日额度装配成响应体；0 原样返回，不限由布尔量表达。
func spaDeckSettingsPayload(deck *store.Deck, budget schedule.DeckBudget) spaDeckSettingsResponse {
	return spaDeckSettingsResponse{
		DeckID:          deck.ID,
		DeckName:        deck.Name,
		NewPerDay:       budget.NewPerDay,
		ReviewsPerDay:   budget.ReviewsPerDay,
		NewUsed:         budget.NewUsed,
		ReviewUsed:      budget.ReviewUsed,
		NewLeft:         budget.NewLeft,
		ReviewLeft:      budget.ReviewLeft,
		NewUnlimited:    budget.NewUnlimited,
		ReviewUnlimited: budget.ReviewUnlimited,
	}
}

// spaDeckSettingsResponse 是 SPA 卡组设置接口的响应体（DESIGN.md §8.1、§3.3）。
//
// NewPerDay / ReviewsPerDay 是卡组列上的原始值：0 表示不限，不是「回落到默认」。
// 因为「0 表示不限」与「今日剩余 0 张」在整数上同形，额外的 NewUnlimited /
// ReviewUnlimited 布尔量显式表达不限，前端据此渲染「不限」而不是 0。
type spaDeckSettingsResponse struct {
	DeckID          uint64 `json:"deck_id"`
	DeckName        string `json:"deck_name"`
	NewPerDay       int    `json:"new_per_day"`
	ReviewsPerDay   int    `json:"reviews_per_day"`
	NewUsed         int    `json:"new_used"`
	ReviewUsed      int    `json:"review_used"`
	NewLeft         int    `json:"new_left"`
	ReviewLeft      int    `json:"review_left"`
	NewUnlimited    bool   `json:"new_unlimited"`
	ReviewUnlimited bool   `json:"review_unlimited"`
}

// spaDeckSettingsRequest 是 PATCH 的请求体；用指针区分「未提供」与「提供了 0」。
// 0 是合法值（不限），因此不能靠零值判断字段是否出现。
type spaDeckSettingsRequest struct {
	NewPerDay     *int `json:"new_per_day"`
	ReviewsPerDay *int `json:"reviews_per_day"`
}

// registerSPADeckSettingsRoutes 挂载 SPA 的卡组每日上限读写接口（仅 owner，写操作过 CSRF）。
//
// 只注册 /api/v1/decks/:id/settings 这两个 JSON 端点，绝不改动 SSR 的
// GET/POST /decks/:id/settings —— 后者仍是同一路径上的权威路由，SPA 页面因此走
// 独立前端路径 /spa/decks/:id/settings，避免在浏览器验证之前遮蔽 SSR 路由。
func (s *Server) registerSPADeckSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/api/v1/decks/:id/settings", s.spaDeckSettingsGet)
	router.PATCH("/api/v1/decks/:id/settings", s.sessions.CSRFMiddleware(), s.spaDeckSettingsPatch)
}

// spaDeckSettingsGet 读取单个卡组的每日上限与今日已用/剩余（仅 owner）。
// 只接受浏览器会话，拒绝 API Key / bearer；额度取自 schedule.DeckBudgets，
// 与复习队列同源，网页层不重算公式。
func (s *Server) spaDeckSettingsGet(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	budget, err := s.deckBudget(c.Request.Context(), user.ID, deck.ID)
	if err != nil {
		s.logger.Error("load deck budget for SPA settings failed", "deck_id", deck.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, spaDeckSettingsPayload(deck, budget))
}

// spaDeckSettingsPatch 保存每日上限（仅 owner）。
// 复用 store.DeckStore.SetCaps（owner 校验与 0 原样落库都在那里）与同一条
// deck.caps_change 审计；非法输入（非数字 / 负数 / 缺字段）一律 400 且不写库、不写审计。
func (s *Server) spaDeckSettingsPatch(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	var req spaDeckSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.NewPerDay == nil || req.ReviewsPerDay == nil {
		spaShareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if *req.NewPerDay < 0 || *req.ReviewsPerDay < 0 {
		// 负值在 handler 层就拒掉，交给 store 会变成 500（ErrInvalidDeckCap）。
		spaShareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	caps := store.DeckCaps{NewPerDay: *req.NewPerDay, ReviewsPerDay: *req.ReviewsPerDay}
	if err := s.decks.SetCaps(c.Request.Context(), user.ID, deck.ID, caps); err != nil {
		s.logger.Error("set deck caps for SPA failed", "deck_id", deck.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	// 与其他卡组变更同口径：改额度也留痕，它决定这个卡组每天向所有使用者放多少张卡出来。
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckCaps,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"new_per_day": caps.NewPerDay, "reviews_per_day": caps.ReviewsPerDay},
	})
	budget, err := s.deckBudget(c.Request.Context(), user.ID, deck.ID)
	if err != nil {
		s.logger.Error("reload deck budget after SPA caps change failed", "deck_id", deck.ID, "error", err)
		spaShareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, spaDeckSettingsPayload(deck, budget))
}
