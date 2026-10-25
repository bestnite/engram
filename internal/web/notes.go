package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/render"
	"git.nite07.com/nite/engram/internal/store"
)

// registerNoteRoutes mounts the card list and edit pages.
//
// Skipped when the dependency is not wired up, so M0-stage tests can still construct a Server. Ownership is checked with the deck owner for now
// (deck_grants has not landed yet; this will be extended to an owner/editor check).
func (s *Server) registerNoteRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil {
		return
	}
	// The three GET pages — card list, edit and create — all return the app shell: the three *Route handlers
	// first authorize by their original session and role rules, then hand over the app shell, and the client router plus JSON endpoints render the page.
	router.GET("/decks/:id/notes", s.noteListRoute)
	router.GET("/decks/:id/notes/:nid", s.noteEditRoute)
	// New card: a separate path prefix, to avoid ambiguity with the /notes/:nid parameterised route.
	router.GET("/decks/:id/new-note", s.noteNewRoute)
	// The SPA's create/edit preview goes over JSON.
	router.POST("/api/v1/decks/:id/notes/preview", s.sessions.CSRFMiddleware(), s.notePreview)
}

// requireUser takes the current signed-in user; when not signed in it redirects to the login page and returns false.
func (s *Server) requireUser(c *gin.Context) (*store.User, bool) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		c.Redirect(http.StatusSeeOther, "/login")
		return nil, false
	}
	return u, true
}

// deckByPublicID resolves the public id and fetches the deck; unknown, empty or invalid all write 404 and return false.
// Deck primary keys are not public and the path parameter carries only the opaque id, so it is swapped for the numeric key here every time.
func (s *Server) deckByPublicID(c *gin.Context, publicID string) (*store.Deck, bool) {
	raw := strings.TrimSpace(publicID)
	if raw == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	deck, err := s.decks.ByPublicID(c.Request.Context(), raw)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	return deck, true
}

// deckIDParam resolves the :id path parameter (a public id string) into the numeric primary key;
// unknown, empty or invalid all 404 (the same shape as "deck does not exist", leaking no existence).
func (s *Server) deckIDParam(c *gin.Context) (uint64, bool) {
	deck, ok := s.deckByPublicID(c, c.Param("id"))
	if !ok {
		return 0, false
	}
	return deck.ID, true
}

// loadDeckForRole fetches the deck and checks that the current user holds at least the want role.
//
// The check itself lives in auth.DeckAccess (shared with REST/MCP, not duplicated); this only
// maps errors to HTML responses: deck not found -> 404, insufficient permission -> 403, and writes one permission.denied
// audit record (who tried to do what to which deck, and when, was refused).
func (s *Server) loadDeckForRole(c *gin.Context, user *store.User, deckID uint64, want string) (*store.Deck, bool) {
	deck, _, ok := s.loadDeckWithRole(c, user, deckID, want)
	return deck, ok
}

// loadDeckWithRole is the same as loadDeckForRole but also returns the caller's effective role on the deck.
func (s *Server) loadDeckWithRole(c *gin.Context, user *store.User, deckID uint64, want string) (*store.Deck, string, bool) {
	if s.access == nil {
		// A missing dependency is a server configuration problem and must not be let through.
		s.logger.Error("deck access checker is not wired", "deck_id", deckID)
		c.AbortWithStatus(http.StatusInternalServerError)
		return nil, "", false
	}
	deck, role, err := s.access.RequireRole(c.Request.Context(), deckID, user.ID, want)
	if err == nil {
		return deck, role, true
	}
	if errors.Is(err, auth.ErrDeckNotFound) {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, "", false
	}
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPermissionDenied,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"required_role": want, "user_role": role},
	})
	c.AbortWithStatus(http.StatusForbidden)
	return nil, "", false
}

// parsePage parses ?page=; invalid or missing is treated as page 1.
func parsePage(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// noteListRoute serves GET /decks/:id/notes: it returns the app shell and the client router renders
// the card list, with data still coming from the existing JSON endpoint (GET /api/v1/decks/:id/notes).
//
// Authorization matches the pre-migration SSR list page item by item: a signed-in session is required first (anonymous visitors are redirected to the login page), then deck readability
// is decided by the reader role — a deck you may not read still returns 403, and nothing is let through by the shell switch.
func (s *Server) noteListRoute(c *gin.Context) {
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

// noteEditRoute serves GET /decks/:id/notes/:nid: it returns the app shell and the client router
// renders the card edit page, with data still coming from the existing JSON endpoints (GET list + PATCH /api/v1/notes/:id).
// Writes from the edit page go over REST inside the SPA, but authorizing the page read is still the server's job.
//
// Authorization matches the pre-migration SSR edit page item by item: a signed-in session is required first (anonymous visitors are redirected to the login page), then deck writability
// is decided by the editor role — a non-editor still gets 403, and the shell switch does not hand the edit shell to an unauthorized user.
func (s *Server) noteEditRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// noteNewRoute serves GET /decks/:id/new-note: it returns the app shell and the client router renders the new-card page,
// while submission still goes through POST /api/v1/decks/:id/notes.
//
// Authorization matches the pre-migration SSR new page item by item: anonymous redirects to the login page, non-editors get 403.
func (s *Server) noteNewRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// PreviewCard is the rendered front and back of one card.
type PreviewCard struct {
	TemplateLabel string
	FrontHTML     string
	BackHTML      string
}

// NotePreviewData is the payload for the card edit page preview.
type NotePreviewData struct {
	Title      string
	FrontLabel string
	BackLabel  string
	Error      string
	Cards      []PreviewCard
}

// buildPreview renders the front and back of every card with internal/render, for the SPA preview JSON.
func (s *Server) buildPreview(loc *i18n.Localizer, kind string, fields map[string]any) NotePreviewData {
	data := NotePreviewData{
		Title:      loc.T("notes.preview.title"),
		FrontLabel: loc.T("notes.preview.front"),
		BackLabel:  loc.T("notes.preview.back"),
	}
	t, ok := cardtype.Lookup(kind)
	if !ok {
		data.Error = loc.T("notes.preview.invalid") + kind
		return data
	}
	cards, err := cardtype.Cards(cardtype.Note{Kind: kind, Fields: fields})
	if err != nil {
		data.Error = loc.T("notes.preview.invalid") + err.Error()
		return data
	}
	for _, card := range cards {
		front, err := t.Render(card, cardtype.SideFront)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		back, err := t.Render(card, cardtype.SideBack)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		frontHTML, err := renderSide(front)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		backHTML, err := renderSide(back)
		if err != nil {
			data.Error = loc.T("notes.preview.invalid") + err.Error()
			return data
		}
		data.Cards = append(data.Cards, PreviewCard{
			TemplateLabel: card.Template,
			FrontHTML:     frontHTML,
			BackHTML:      backHTML,
		})
	}
	return data
}

// renderSide renders one side's Markdown text into sanitized HTML, with a choice card's options appended after the body.
// It serves the pages that show the options as text (note preview and public share); the review page draws them as answer
// controls instead and therefore renders the body alone.
func renderSide(side cardtype.RenderResult) (string, error) {
	body := side.Body
	if len(side.Options) > 0 {
		body = strings.Join(append([]string{body}, side.Options...), "\n\n")
	}
	return render.RenderMarkdown(body)
}
