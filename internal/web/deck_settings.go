package web

import (
	"context"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// registerDeckSettingsRoutes mounts the deck settings page (owner only): change the daily new-card / review limits.
//
// The gating is the same as registerDeckRoutes: the /decks/:id/settings link rendered on the list page and the route registered here
// must share the same dependency precondition, otherwise you get "the link is there but clicking it is 404". The SSR page layer has been removed: the page only serves the app shell,
// and reads and writes go through the /api/v1/decks/:id/settings JSON endpoint (deck_settings_api.go).
func (s *Server) registerDeckSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/decks/:id/settings", s.deckSettingsRoute)
}

// deckBudget gets one deck's quota for today via schedule.DeckBudgets (the same source as the review queue).
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

// deckSettingsRoute serves GET /decks/:id/settings: it only serves the SPA app shell,
// and the client router renders the deck daily limits page; reads and writes go through /api/v1/decks/:id/settings (the same service logic and audit).
//
// The gate stays server-side: the page edits the caller's own study settings on that deck, so any member of the deck (reader and above)
// can open it; a deck they have no access to and a nonexistent deck end with 403 / 404 before the app shell is returned.
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
