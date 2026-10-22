package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/store"
)

// registerShareBrowseRoutes mounts the registration-free read-only browse page.
//
// GET /s/:token returns the app shell and the client router renders the read-only browse page; content and passphrase unlocking go through the api/v1/share/*
// JSON endpoints (share_api.go). The link itself (optionally with a passphrase) is the credential, so no sign-in is required.
func (s *Server) registerShareBrowseRoutes(router *gin.Engine) {
	if s.shareLinks == nil || s.decks == nil || s.notes == nil {
		return
	}
	router.GET("/s/:token", s.shareBrowseRoute)
	// The SPA's same-origin JSON transport (share_api.go); reachability and media authorization are unchanged.
	s.registerShareRoutes(router)
}

// resolveShareLink resolves the plaintext token in the path and fetches the deck; on failure it writes 404 and returns false.
// Revoked, expired or nonexistent all 404 without distinguishing the three, so token history does not leak.
func (s *Server) resolveShareLink(c *gin.Context) (*store.ShareLink, *store.Deck, bool) {
	link, err := s.shareLinks.Resolve(c.Request.Context(), c.Param("token"), time.Now().UTC())
	if err != nil {
		// ErrShareLinkNotFound and other errors are all handled as 404: internal error details are not leaked to anonymous visitors.
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	}
	deck, err := s.decks.ByID(c.Request.Context(), link.DeckID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	}
	return link, deck, true
}

// recordShareGrant records "this server-side session opened that deck" (L3):
// a deck a signed-in user opened through a share link counts towards their session-scoped visible set, which is what lets media reads (GET /media/<sha256>) through.
//
// The expiry is the earlier of the link expiry and the session expiry (store.ShareSessionStore.Grant). A failed grant only logs one English line and does
// not block browsing: the page content can already render, and the consequence of the failure is missing images, which should not turn the whole browse page into a 500.
// Anonymous visitors have no server-side session and nowhere to record — they cannot read media anyway (/media requires sign-in), so skip it.
func (s *Server) recordShareGrant(c *gin.Context, deck *store.Deck, link *store.ShareLink) {
	if s.shareSessions == nil {
		return
	}
	sess, ok := auth.CurrentSession(c)
	if !ok {
		return
	}
	if err := s.shareSessions.Grant(c.Request.Context(), sess.ID, deck.ID, link.ExpiresAt, sess.ExpiresAt); err != nil {
		s.logger.Error("record share session grant failed", "deck_id", deck.ID, "error", err)
	}
}

// shareNoteView renders all cards of a note into front/back HTML (the same rendering pipeline as the preview).
// Returns sanitized front/back HTML; a failure rendering any single card returns an error, and the caller decides whether to skip.
func (s *Server) shareNoteView(note store.Note) (front, back string, err error) {
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return "", "", err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return "", "", cardtype.UnknownKindError(note.Kind)
	}
	cards, err := cardtype.Cards(cardtype.Note{Kind: note.Kind, Fields: fields})
	if err != nil {
		return "", "", err
	}
	var frontParts, backParts []string
	for _, card := range cards {
		f, err := t.Render(card, cardtype.SideFront)
		if err != nil {
			return "", "", err
		}
		b, err := t.Render(card, cardtype.SideBack)
		if err != nil {
			return "", "", err
		}
		frontHTML, err := renderSide(f)
		if err != nil {
			return "", "", err
		}
		backHTML, err := renderSide(b)
		if err != nil {
			return "", "", err
		}
		frontParts = append(frontParts, frontHTML)
		backParts = append(backParts, backHTML)
	}
	return strings.Join(frontParts, "\n"), strings.Join(backParts, "\n"), nil
}
