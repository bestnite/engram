package web

import (
	"context"
	"net/http"

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

// deckQueueCount carries two independent pairs of numbers for one deck.
// NewCount / ReviewCount are what can be studied today, after the daily allowances.
// StudiedCount / RemainingCount are the overall progress through the deck, which no daily allowance limits.
type deckQueueCount struct {
	DeckID         string `json:"deck_id"`
	NewCount       int    `json:"new_count"`
	ReviewCount    int    `json:"review_count"`
	StudiedCount   int    `json:"studied_count"`
	RemainingCount int    `json:"remaining_count"`
}

// deckQueueCountsAPI returns the queue counts and the study progress of the decks visible to the current session,
// directly reusing the shared queue builder.
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
	decks, err := s.decks.ListVisible(ctx, user.ID)
	if err != nil {
		s.logger.Error("list decks for queue counts failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	counts, err := s.deckQueueCounts(ctx, user, decks)
	if err != nil {
		s.logger.Error("count deck queue failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	progress, err := schedule.NewQueueBuilder(s.db, s.decks, nil).DeckProgress(ctx, user.ID, deckIDs(decks))
	if err != nil {
		s.logger.Error("count deck progress failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	response := deckQueueCountsResponse{Decks: make([]deckQueueCount, 0, len(decks))}
	for _, deck := range decks {
		count := counts[deck.ID]
		p := progress[deck.ID]
		response.Decks = append(response.Decks, deckQueueCount{
			DeckID: deck.PublicID, NewCount: count.New, ReviewCount: count.Review,
			StudiedCount: p.Studied, RemainingCount: p.Remaining,
		})
	}
	c.JSON(http.StatusOK, response)
}

// deckQueueCounts gets the "studyable today" makeup of each deck on the list page (the two numbers new / review) via schedule.DeckCounts:
// it shares the filter conditions and quota formula with /review's card-fetching path, so the two numbers summed equal the count actually studyable after clicking in.
// The count does not compute retrievability and needs no scheduler; "used today" is tallied by the user's own timezone and day boundary.
func (s *Server) deckQueueCounts(ctx context.Context, user *store.User, decks []store.Deck) (map[uint64]schedule.DeckQueueCounts, error) {
	return schedule.NewQueueBuilder(s.db, s.decks, nil).DeckCounts(ctx, user.ID, deckIDs(decks), schedule.QueueOptions{
		Timezone:      user.Timezone,
		DayCutoffHour: user.DayCutoffHour,
	})
}

// deckIDs collects the internal ids of the listed decks.
func deckIDs(decks []store.Deck) []uint64 {
	ids := make([]uint64, 0, len(decks))
	for i := range decks {
		ids = append(ids, decks[i].ID)
	}
	return ids
}
