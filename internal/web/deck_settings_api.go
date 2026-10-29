package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// deckSettingsPayload assembles the deck and the caller's own quota for today into the response body; 0 is returned as-is and "unlimited" is expressed by booleans.
// deck_id / preset_id are public ids: the deck's numeric primary key is not exposed, likewise for presets, and they are passed in by the caller here.
func deckSettingsPayload(deck *store.Deck, role, presetPublicID string, budget schedule.DeckBudget) deckSettingsResponse {
	return deckSettingsResponse{
		DeckID:          deck.PublicID,
		DeckName:        deck.Name,
		DeckDescription: deck.Description,
		Role:            role,
		PresetID:        presetPublicID,
		NewPerDay:       budget.NewPerDay,
		ReviewsPerDay:   budget.ReviewsPerDay,
		NewExtra:        budget.NewExtra,
		NewUsed:         budget.NewUsed,
		ReviewUsed:      budget.ReviewUsed,
		NewLeft:         budget.NewLeft,
		ReviewLeft:      budget.ReviewLeft,
		NewUnlimited:    budget.NewUnlimited,
		ReviewUnlimited: budget.ReviewUnlimited,
	}
}

// deckSettingsResponse is the response body of the SPA deck settings API: the caller's own study settings on that deck and the quota for today.
// The owner and each sharing member each have their own settings, unaffected by one another (see store.StudySettings).
//
// NewPerDay / ReviewsPerDay are the raw values from the settings: 0 means unlimited, not "fell back to the default".
// Because "0 means unlimited" and "0 cards left today" look identical as integers, the extra NewUnlimited /
// ReviewUnlimited booleans express "unlimited" explicitly, and the frontend renders "unlimited" rather than 0 based on them.
type deckSettingsResponse struct {
	DeckID   string `json:"deck_id"`
	DeckName string `json:"deck_name"`
	// DeckDescription lets the owner pre-fill the "deck info" form on the settings page; sharing members cannot change it, and reading it does no harm.
	DeckDescription string `json:"deck_description"`
	// Role is the caller's role on the deck (owner / editor / reader), and the page uses it to decide whether to show owner-only entries.
	Role          string `json:"role"`
	PresetID      string `json:"preset_id"`
	NewPerDay     int    `json:"new_per_day"`
	ReviewsPerDay int    `json:"reviews_per_day"`
	// NewExtra is the extra new-card quota added by today's "one more round"; NewLeft already includes it, NewPerDay does not.
	NewExtra        int  `json:"new_extra"`
	NewUsed         int  `json:"new_used"`
	ReviewUsed      int  `json:"review_used"`
	NewLeft         int  `json:"new_left"`
	ReviewLeft      int  `json:"review_left"`
	NewUnlimited    bool `json:"new_unlimited"`
	ReviewUnlimited bool `json:"review_unlimited"`
}

// deckSettingsRequest is the PATCH request body; pointers distinguish "not provided" from "0 provided".
// 0 is a legal value (unlimited), so the zero value cannot be used to detect whether a field is present. PresetID is a public id string.
type deckSettingsRequest struct {
	NewPerDay     *int    `json:"new_per_day"`
	ReviewsPerDay *int    `json:"reviews_per_day"`
	PresetID      *string `json:"preset_id"`
}

// registerDeckSettingsAPIRoutes mounts the SPA's deck study settings read/write API (any deck member; writes go through CSRF).
//
// Only these two /api/v1/decks/:id/settings JSON endpoints are registered; GET /decks/:id/settings only serves the app shell
// (see deck_settings.go), and all reads and writes of deck limits go through here.
func (s *Server) registerDeckSettingsAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/api/v1/decks/:id/settings", s.deckSettingsGet)
	router.PATCH("/api/v1/decks/:id/settings", s.sessions.CSRFMiddleware(), s.deckSettingsPatch)
}

// deckSettingsGet reads the caller's study settings on a single deck and today's used/remaining (any deck member).
// Accepts browser sessions only and rejects API Key / bearer; the quota comes from schedule.DeckBudgets,
// the same source as the review queue, and the web layer does not recompute the formula.
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

// writeDeckSettings reads back the caller's study settings and quota and writes the response.
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

// deckSettingsPatch saves the caller's study settings on that deck (any deck member; affects only the caller).
// The preset must be the caller's own; invalid input (non-numeric / negative / missing field / someone else's preset) is always 400 with no DB write and no audit row.
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
	// The preset is optional: a request that only changes quotas does not carry it.
	var presetID *uint64
	var presetPublic string
	if req.PresetID != nil {
		preset, err := s.presets.ByPublicID(ctx, strings.TrimSpace(*req.PresetID))
		if err != nil {
			// An unknown or empty preset public id looks the same as "preset unavailable": 400, no DB write, no audit row.
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
		s.auditor.Record(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionDeckPreset,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"preset_id": presetPublic},
		})
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckCaps,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"new_per_day": caps.NewPerDay, "reviews_per_day": caps.ReviewsPerDay},
	})
	// Read back once before assembling the response: the writes above happened in the DB, so using the deck row read before them would echo the old settings.
	updated, err := s.decks.ByID(ctx, deck.ID)
	if err != nil {
		s.logger.Error("reload deck after SPA settings change failed", "deck_id", deck.ID, "error", err)
		shareError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.writeDeckSettings(c, user, updated, role)
}
