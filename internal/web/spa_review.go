package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

type spaReviewRequest struct {
	CardID          uint64   `json:"card_id"`
	Rating          int      `json:"rating"`
	ExpectedVersion int      `json:"expected_version"`
	ElapsedMS       *int     `json:"elapsed_ms"`
	Deck            []uint64 `json:"deck"`
}

// spaReviewAnswer 为 SPA 提供会话 CSRF 保护的答题入口，业务提交与队列仍复用 API service。
func (s *Server) spaReviewAnswer(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unauthorized", "message": "Authentication is required."}})
		return
	}
	var req spaReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CardID == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "Invalid review request."}})
		return
	}
	for _, deckID := range req.Deck {
		if deckID == 0 {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "Invalid deck scope."}})
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
			return
		}
	}
	card, err := s.cards.ByID(c.Request.Context(), req.CardID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "Card not found."}})
		return
	}
	note, err := s.notes.ByID(c.Request.Context(), card.NoteID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "Note not found."}})
		return
	}
	if len(req.Deck) > 0 {
		inScope := false
		for _, deckID := range req.Deck {
			if note.DeckID == deckID {
				inScope = true
				break
			}
		}
		if !inScope {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "Card is outside the selected review range."}})
			return
		}
	}
	if _, graded := graderFor(note.Kind); graded {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "This card requires an answer rather than self-assessment."}})
		return
	}
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: req.Rating, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS, GradeSource: "self",
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	cards, err := s.api.DueCards(c.Request.Context(), user, req.Deck, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"card_id": result.CardID, "review_id": result.ReviewID, "state": result.State,
		"due_at": result.DueAt, "version": result.Version, "stability": result.Stability, "cards": cards, "remaining": len(cards)})
}

func apiError(err error) *api.ServiceError {
	var se *api.ServiceError
	if errors.As(err, &se) {
		return se
	}
	return &api.ServiceError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "The operation failed."}
}
