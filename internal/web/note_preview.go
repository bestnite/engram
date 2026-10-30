package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

type notePreviewRequest struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields"`
}

// notePreview returns the card faces the submitted payload would produce.
func (s *Server) notePreview(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	// The path parameter is the public id; deck primary keys are not public, so swap in the numeric primary key before checking permissions.
	deck, err := s.decks.ByPublicID(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleEditor); !ok {
		return
	}
	var req notePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Fields == nil {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	cards, ok := buildPreview(req.Kind, req.Fields)
	if !ok || len(cards) == 0 {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	// Preview renders the same card faces as the review path. The base travels on every response; the relative media
	// names are handed out only when the caller declares the HTML is for the frame, because without the frame's <base> a
	// relative name would resolve against the SPA page.
	mediaBase := s.frameMediaBase(c.Request.Context(), user.ID, deck.ID)
	forFrame := frameOutputRequested(c) && mediaBase != ""
	result := make([]gin.H, 0, len(cards))
	for _, card := range cards {
		front, back := card.Front, card.Back
		front.MediaBase, back.MediaBase = mediaBase, mediaBase
		if !forFrame {
			front.HTML = absoluteMediaRefs(front.HTML)
			back.HTML = absoluteMediaRefs(back.HTML)
		}
		result = append(result, gin.H{
			"front":      front,
			"back":       back,
			"front_html": front.HTML,
			"back_html":  back.HTML,
		})
	}
	c.JSON(http.StatusOK, gin.H{"cards": result, "media_base": mediaBase})
}

func writeRenderError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code": code, "message": api.ErrorMessage(c.Request.Context(), code),
	}})
}
