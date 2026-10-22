package web

import (
	"context"
	"net/http"
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
	// SPA deck counts accept only the current session, so that a session-only visibility scope is not exposed to API Keys.
	router.GET("/api/v1/decks/queue-counts", s.deckQueueCountsAPI)
}

// registerDeckRoutes mounts the deck list page.
// Skipped when dependencies are not wired up, so M0-stage tests can still construct Server.
func (s *Server) registerDeckRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	// GET /decks has moved to the SPA canonical path: deckListRoute returns the app shell, the client router renders the deck list,
	// and the data goes through the existing JSON endpoints.
	router.GET("/decks", s.deckListRoute)
}

// deckListRoute serves GET /decks: returns the app shell, the client router renders the deck list,
// and the data goes through GET /api/v1/decks and GET /api/v1/decks/queue-counts.
//
// Like the pre-migration SSR list page it requires a signed-in session first: anyone anonymous is always redirected to the login page; the page migration does not change authorization,
// nor add any write path.
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
	DeckID      string `json:"deck_id"`
	NewCount    int    `json:"new_count"`
	ReviewCount int    `json:"review_count"`
}

// deckQueueCountsAPI returns the queue counts of the decks visible to the current session, directly reusing the shared queue builder.
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
	counts, err := s.deckQueueCounts(ctx, user, summaries)
	if err != nil {
		s.logger.Error("count deck queue failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	response := deckQueueCountsResponse{Decks: make([]deckQueueCount, 0, len(summaries))}
	for _, summary := range summaries {
		count := counts[summary.Deck.ID]
		response.Decks = append(response.Decks, deckQueueCount{
			DeckID: summary.Deck.PublicID, NewCount: count.New, ReviewCount: count.Review,
		})
	}
	c.JSON(http.StatusOK, response)
}

// deckQueueCounts gets the "studyable today" makeup of each deck on the list page (the two numbers new / review) via schedule.DeckCounts:
// it shares the filter conditions and quota formula with /review's card-fetching path, so the two numbers summed equal the count actually studyable after clicking in.
// The count does not compute retrievability and needs no scheduler; "used today" is tallied by the user's own timezone and day boundary.
func (s *Server) deckQueueCounts(ctx context.Context, user *store.User, summaries []store.DeckSummary) (map[uint64]schedule.DeckQueueCounts, error) {
	ids := make([]uint64, 0, len(summaries))
	for i := range summaries {
		ids = append(ids, summaries[i].Deck.ID)
	}
	return schedule.NewQueueBuilder(s.db, s.decks, nil).DeckCounts(ctx, user.ID, ids, schedule.QueueOptions{
		Timezone:      user.Timezone,
		DayCutoffHour: user.DayCutoffHour,
	})
}

// resolvePresetID resolves a preset's public id: it must belong to the current user; when absent or invalid it falls back to the first preset.
// store.EnsureDefaultPreset guarantees at least one preset exists, so there is no "no preset to fall back to" branch.
func (s *Server) resolvePresetID(ctx context.Context, userID uint64, raw string) (uint64, error) {
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		return 0, err
	}
	if raw = strings.TrimSpace(raw); raw != "" {
		if p, err := s.presets.ByPublicID(ctx, raw); err == nil && p.OwnerUserID == userID {
			return p.ID, nil
		}
	}
	return presets[0].ID, nil
}
