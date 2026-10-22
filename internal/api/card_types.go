package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// CardTypesResponse is the response body of card-type metadata; kinds are sorted by kind, so the result is stable.
type CardTypesResponse struct {
	Kinds []cardtype.Description `json:"kinds"`
}

// CardTypes returns the self-description of every card type (static metadata, no user state); shared by REST and MCP.
// The frontend renders edit forms and review controls from it, and agents learn which fields each card type has and which are required.
func (a *API) CardTypes() CardTypesResponse {
	return CardTypesResponse{Kinds: cardtype.Descriptions()}
}

// cardTypes is GET /api/v1/card-types, scope: read.
func (a *API) cardTypes(c *gin.Context) {
	c.JSON(http.StatusOK, a.CardTypes())
}
