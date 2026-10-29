package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// registerCloneRoutes mounts deck cloning. Cloning is open to decks "you can read" (reader and above):
// it copies the content into the caller's account; the write operation goes through the CSRF middleware. Skipped when dependencies are not wired up.
func (s *Server) registerCloneRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.presets == nil {
		return
	}
	router.POST("/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
	router.POST("/api/v1/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
}

// deckClone copies a deck "you can read" into the current account.
//
// Content is copied (note + card), progress does not follow (the new cards have no card_states rows); share grants are not copied,
// so the clone has only the caller as owner. A cloned deck keeps the preset that is in effect for the caller on the source deck: cloning someone else's deck
// does not copy the owner's scheduling parameters (including optimized weights) to the caller; learning parameters are always each person's own.
func (s *Server) deckClone(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	// Anyone at reader or above can clone: if you can see the content you can take it away and study your own progress.
	src, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	settings, err := s.decks.StudySettings(ctx, user.ID, src)
	if err != nil {
		s.logger.Error("load study settings for deck clone failed", "deck_id", src.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presetID := settings.PresetID
	// The copy name = source name + localized suffix; store.DeckCopyName truncates the source name by rune,
	// ensuring a legal source name close to the limit does not produce an over-long copy name.
	suffix := loc.Tf("clone.name_suffix", nil)
	name := store.DeckCopyName(src.Name, suffix)
	cloned, err := s.decks.Clone(ctx, src, user.ID, name, presetID)
	if err != nil {
		s.logger.Error("clone deck failed", "deck_id", src.ID, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckClone,
		TargetType: "deck",
		TargetID:   store.Ptr(cloned.ID),
		Detail:     map[string]any{"source_deck_id": src.PublicID, "preset_id": cloned.PresetID},
	})
	if acceptsJSON(c) {
		c.JSON(http.StatusCreated, gin.H{"id": cloned.PublicID, "name": cloned.Name})
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%s/notes", cloned.PublicID))
}

// acceptsJSON reports whether the request asks for a JSON response (the SPA's fetch calls).
//
// It only looks at the application/json media type in the Accept header, allowing it to carry parameters or appear in a candidate list
// (such as "application/json; charset=utf-8" or "application/json, text/plain, */*"):
// with an exact whole-string comparison, as soon as a client appends a charset the JSON call would be misjudged as a form submission, so
// the fetch would follow the 303, get HTML and fail to parse it as JSON. The Accept of a browser form submission does not contain
// application/json, so it still gets the 303 redirect.
func acceptsJSON(c *gin.Context) bool {
	for _, part := range strings.Split(c.GetHeader("Accept"), ",") {
		media := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(media, "application/json") {
			return true
		}
	}
	return false
}
