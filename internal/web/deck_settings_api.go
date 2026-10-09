package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// deckSettingsPayload 把卡组与调用者自己的今日额度装配成响应体；0 原样返回，不限由布尔量表达。
// deck_id / preset_id 是对外 id：卡组的数字主键不对外，预设同理，这里由调用方传进来。
func deckSettingsPayload(deck *store.Deck, role, presetPublicID string, budget schedule.DeckBudget) deckSettingsResponse {
	return deckSettingsResponse{
		DeckID:          deck.PublicID,
		DeckName:        deck.Name,
		DeckDescription: deck.Description,
		Role:            role,
		PresetID:        presetPublicID,
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

// deckSettingsResponse 是 SPA 卡组设置接口的响应体：调用者自己在该卡组上的学习设置与今日额度。
// 属主与共享成员各有一份设置，互不影响（见 store.StudySettings）。
//
// NewPerDay / ReviewsPerDay 是设置里的原始值：0 表示不限，不是「回落到默认」。
// 因为「0 表示不限」与「今日剩余 0 张」在整数上同形，额外的 NewUnlimited /
// ReviewUnlimited 布尔量显式表达不限，前端据此渲染「不限」而不是 0。
type deckSettingsResponse struct {
	DeckID   string `json:"deck_id"`
	DeckName string `json:"deck_name"`
	// DeckDescription 供属主在设置页预填「卡组信息」表单；共享成员改不了它，读到也无妨。
	DeckDescription string `json:"deck_description"`
	// Role 是调用者在卡组上的角色（owner / editor / reader），页面据此决定是否展示属主才有的入口。
	Role            string `json:"role"`
	PresetID        string `json:"preset_id"`
	NewPerDay       int    `json:"new_per_day"`
	ReviewsPerDay   int    `json:"reviews_per_day"`
	NewUsed         int    `json:"new_used"`
	ReviewUsed      int    `json:"review_used"`
	NewLeft         int    `json:"new_left"`
	ReviewLeft      int    `json:"review_left"`
	NewUnlimited    bool   `json:"new_unlimited"`
	ReviewUnlimited bool   `json:"review_unlimited"`
}

// deckSettingsRequest 是 PATCH 的请求体；用指针区分「未提供」与「提供了 0」。
// 0 是合法值（不限），因此不能靠零值判断字段是否出现。PresetID 是对外 id 字符串。
type deckSettingsRequest struct {
	NewPerDay     *int    `json:"new_per_day"`
	ReviewsPerDay *int    `json:"reviews_per_day"`
	PresetID      *string `json:"preset_id"`
}

// registerDeckSettingsAPIRoutes 挂载 SPA 的卡组学习设置读写接口（卡组任何成员，写操作过 CSRF）。
//
// 只注册 /api/v1/decks/:id/settings 这两个 JSON 端点；GET /decks/:id/settings 只发应用壳
// （见 deck_settings.go），卡组上限的读写全部走这里。
func (s *Server) registerDeckSettingsAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/api/v1/decks/:id/settings", s.deckSettingsGet)
	router.PATCH("/api/v1/decks/:id/settings", s.sessions.CSRFMiddleware(), s.deckSettingsPatch)
}

// deckSettingsGet 读取调用者在单个卡组上的学习设置与今日已用/剩余（卡组任何成员）。
// 只接受浏览器会话，拒绝 API Key / bearer；额度取自 schedule.DeckBudgets，
// 与复习队列同源，网页层不重算公式。
func (s *Server) deckSettingsGet(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	deck, role, ok := s.loadDeckWithRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	s.writeDeckSettings(c, user, deck, role)
}

// writeDeckSettings 读出调用者的学习设置与额度并写响应。
func (s *Server) writeDeckSettings(c *gin.Context, user *store.User, deck *store.Deck, role string) {
	ctx := c.Request.Context()
	settings, err := s.decks.StudySettings(ctx, user.ID, deck)
	if err != nil {
		s.logger.Error("load study settings for SPA settings failed", "deck_id", deck.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	budget, err := s.deckBudget(ctx, user, deck.ID)
	if err != nil {
		s.logger.Error("load deck budget for SPA settings failed", "deck_id", deck.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, deckSettingsPayload(deck, role, s.presetPublicID(ctx, settings.PresetID), budget))
}

// deckSettingsPatch 保存调用者在该卡组上的学习设置（卡组任何成员；只影响调用者自己）。
// 预设必须是调用者自己的；非法输入（非数字 / 负数 / 缺字段 / 别人的预设）一律 400 且不写库、不写审计。
func (s *Server) deckSettingsPatch(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	deck, role, ok := s.loadDeckWithRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	var req deckSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.NewPerDay == nil || req.ReviewsPerDay == nil {
		shareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if *req.NewPerDay < 0 || *req.ReviewsPerDay < 0 {
		shareError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	// 预设是可选项：只改额度的请求不带它。
	var presetID *uint64
	var presetPublic string
	if req.PresetID != nil {
		preset, err := s.presets.ByPublicID(ctx, strings.TrimSpace(*req.PresetID))
		if err != nil {
			// 未知或空串的预设对外 id 与「预设不可用」同形：400，不写库、不写审计。
			shareError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		presetID = &preset.ID
		presetPublic = preset.PublicID
	}
	caps := store.DeckCaps{NewPerDay: *req.NewPerDay, ReviewsPerDay: *req.ReviewsPerDay}
	if err := s.decks.SetStudySettings(ctx, user.ID, deck, presetID, &caps); err != nil {
		if errors.Is(err, store.ErrDeckPresetInvalid) {
			shareError(c, http.StatusBadRequest, "invalid_request")
			return
		}
		s.logger.Error("set study settings for SPA failed", "deck_id", deck.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	if presetID != nil {
		s.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionDeckPreset,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"preset_id": presetPublic},
		})
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckCaps,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"new_per_day": caps.NewPerDay, "reviews_per_day": caps.ReviewsPerDay},
	})
	// 回读一次再组响应：上面的写入都发生在库上，用写入前读到的卡组行会回显旧的设置。
	updated, err := s.decks.ByID(ctx, deck.ID)
	if err != nil {
		s.logger.Error("reload deck after SPA settings change failed", "deck_id", deck.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.writeDeckSettings(c, user, updated, role)
}
