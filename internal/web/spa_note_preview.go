package web

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

type notePreviewRequest struct {
	Kind   string         `json:"kind"`
	Fields map[string]any `json:"fields"`
}

// notePreview 只返回 internal/render 清洗过的卡面 HTML。
func (s *Server) notePreview(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	deckID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || deckID == 0 {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	var req notePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Fields == nil {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	preview := s.buildPreview(loc, req.Kind, req.Fields)
	if preview.Error != "" || len(preview.Cards) == 0 {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	result := make([]gin.H, 0, len(preview.Cards))
	for _, card := range preview.Cards {
		result = append(result, gin.H{"front_html": card.FrontHTML, "back_html": card.BackHTML})
	}
	c.JSON(http.StatusOK, gin.H{"cards": result})
}

func writeRenderError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"code": code, "message": api.ErrorMessage(c.Request.Context(), code),
	}})
}
