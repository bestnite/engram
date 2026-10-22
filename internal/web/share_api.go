package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA same-origin JSON transport for public read-only share browsing, plus the GET shell switch.
//
// The security boundary matches the pre-migration one item by item; the shell switch neither loosens nor tightens it:
//   - revoked, expired or nonexistent is always 404, without telling the three apart (no leaking token history);
//   - a password-protected link returns no card content before unlocking, only "password required + deck name";
//   - card fronts and backs always reuse the server-side sanitizing render pipeline (shareNoteView → renderSide), and the client only hands sanitized
//     HTML to {@html}, never rendering raw fields as trusted Markdown;
//   - a successful open registers the session-level media read grant (recordShareGrant), and media readability is still decided by /media's login gate.
//
// Anonymous access is unchanged: the share link itself is still the credential, GET /s/:token never requires login, and neither does this.

// apiShareNote is the sanitized HTML of both sides of one shared card.
type apiShareNote struct {
	FrontHTML string `json:"front_html"`
	BackHTML  string `json:"back_html"`
}

// apiShareResponse is the JSON shape of share browse content.
// When password_required is true, notes is always empty, never returning content from before the unlock.
type apiShareResponse struct {
	DeckName         string         `json:"deck_name"`
	PasswordRequired bool           `json:"password_required"`
	Notes            []apiShareNote `json:"notes"`
}

// registerShareRoutes mounts the SPA JSON endpoints for share browsing; password unlock uses a CSRF-free POST (same reason as SSR:
// there is no server-side session at that point to bind a token to, and no state is produced beyond the rendered content).
func (s *Server) registerShareRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/api/v1/share/:token", s.shareGet)
	router.POST("/api/v1/share/:token/unlock", s.shareUnlock)
	// Joining a deck writes a grant row (a state change) and is open to logged-in users only, so it goes through the CSRF middleware.
	if s.sessions != nil {
		router.POST("/api/v1/share/:token/join", s.sessions.CSRFMiddleware(), s.shareJoin)
	}
}

// shareBrowseRoute serves GET /s/:token: it returns the app shell, and the client router renders the read-only browse page;
// the content goes through GET /api/v1/share/:token (the same sanitizing render and media grant).
//
// The reachability decision must stay before switching to the shell: a revoked, expired or nonexistent link still returns 404, matching this URL's pre-migration behavior.
func (s *Server) shareBrowseRoute(c *gin.Context) {
	if _, _, ok := s.resolveShareLink(c); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// shareGet returns the read-only content of one shared deck (GET /api/v1/share/:token).
//
// A link without a password returns the content directly; a password-protected link only returns {password_required: true} before unlock, without leaking card text.
func (s *Server) shareGet(c *gin.Context) {
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash != nil {
		c.JSON(http.StatusOK, apiShareResponse{
			DeckName:         deck.Name,
			PasswordRequired: true,
			Notes:            []apiShareNote{},
		})
		return
	}
	s.shareContent(c, deck, link)
}

// shareUnlock checks the password and returns the content (POST /api/v1/share/:token/unlock).
//
// A link without a password is equivalent to browsing directly (idempotent); a wrong password returns 401 with a stable code.
// This deliberately consumes no token and keeps no unlock state — every request validates afresh, so it is stateless.
func (s *Server) shareUnlock(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	link, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	if link.PasswordHash == nil {
		s.shareContent(c, deck, link)
		return
	}
	matched, err := auth.Verify(*link.PasswordHash, req.Password)
	if err != nil {
		s.logger.Error("verify share link password failed", "deck_id", deck.ID, "error", err)
		apiAuthError(c, http.StatusUnauthorized, "share_password_invalid", "The password is incorrect.")
		return
	}
	if !matched {
		s.logger.Info("share link password rejected", "deck_id", deck.ID)
		apiAuthError(c, http.StatusUnauthorized, "share_password_invalid", "The password is incorrect.")
		return
	}
	s.shareContent(c, deck, link)
}

// shareContent assembles the read-only content JSON: it registers the session-level media grant, then lists the cards and sanitizes-renders them.
// A single bad card is skipped with an English log line and does not block the rest of the content.
func (s *Server) shareContent(c *gin.Context, deck *store.Deck, link *store.ShareLink) {
	ctx := c.Request.Context()
	// Access has succeeded (no password, or the password check passed) → register "this session opened this deck",
	// and only then are the browser's later /media/<sha256> requests allowed through; an anonymous visitor has no server-side session, so it is skipped.
	s.recordShareGrant(c, deck, link)
	notes, _, err := s.notes.List(ctx, store.NoteListOptions{
		DeckID: deck.ID, Page: 1, PerPage: store.DefaultNotePageSize, Status: store.NoteStatusActive,
	})
	if err != nil {
		s.logger.Error("list notes for share browse failed", "deck_id", deck.ID, "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	rendered := make([]apiShareNote, 0, len(notes))
	for i := range notes {
		front, back, err := s.shareNoteView(notes[i])
		if err != nil {
			// A single bad card does not block browsing: it is skipped with an English log line and the rest is shown as usual.
			s.logger.Error("render shared note failed", "note_id", notes[i].ID, "error", err)
			continue
		}
		rendered = append(rendered, apiShareNote{FrontHTML: front, BackHTML: back})
	}
	c.JSON(http.StatusOK, apiShareResponse{
		DeckName:         deck.Name,
		PasswordRequired: false,
		Notes:            rendered,
	})
}

// shareJoin lets "a logged-in user holding the link" add this deck to their own list (POST /api/v1/share/:token/join).
//
// Why joining is an explicit action instead of happening automatically on opening the link: links get forwarded and clicked casually in group chats,
// and "opening once" only means agreeing to look once, not agreeing to have a deck pushed into my list and review queue — once in the list
// it eats into my daily new-card quota. So opening a link only registers a session-level read-only grant (recordShareGrant, living and dying with the link),
// and joining is decided by the user pressing a button; the token is the credential for that join, and whoever lacks the link gets no grant.
//
// After joining, that grant is independent of the link (revoking or expiring the link does not take it back); the owner revokes members one by one on the sharing page —
// it is exactly the same kind of grant as one obtained by "invite accepted", revoked through the same path.
//
// Failure semantics: link revoked / expired / nonexistent → 404 (the three are not told apart, no leaking token history);
// not logged in → 401 (the frontend sends a logged-out visitor to log in and does not show this button); the owner pressing it, or an existing grant
// (editor included) → idempotent success, writing no redundant grant row and not downgrading the role to reader.
func (s *Server) shareJoin(c *gin.Context) {
	if s.grants == nil {
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
		return
	}
	user, ok := auth.CurrentUser(c)
	if !ok || user == nil {
		apiAuthError(c, http.StatusUnauthorized, api.CodeUnauthorized, "Authentication is required.")
		return
	}
	// The reachability decision is the same as the browse page: revoked/expired/nonexistent is always 404.
	_, deck, ok := s.resolveShareLink(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	joinable := deck.OwnerUserID != user.ID
	if joinable {
		existing, err := s.grants.Role(ctx, deck.ID, user.ID)
		if err != nil {
			s.logger.Error("read deck grant for share join failed", "deck_id", deck.ID, "user_id", user.ID, "error", err)
			apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
			return
		}
		joinable = !store.ValidRole(existing)
	}
	if joinable {
		if err := s.grants.Grant(ctx, deck.ID, user.ID, store.RoleReader, nil); err != nil {
			s.logger.Error("join deck via share link failed", "deck_id", deck.ID, "user_id", user.ID, "error", err)
			apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionShareLinkJoin,
			TargetType: "deck",
			TargetID:   store.Ptr(deck.ID),
			Detail:     map[string]any{"role": store.RoleReader},
		})
	}
	c.JSON(http.StatusOK, gin.H{"deck_id": deck.ID, "joined": true})
}
