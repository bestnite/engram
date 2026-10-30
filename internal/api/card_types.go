package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CardTypesResponse is the response body of card-type metadata; kinds are sorted by kind, so the result is stable.
type CardTypesResponse struct {
	Kinds []cardKindDescription `json:"kinds"`
}

// CardTypes returns the self-description of every card kind (static metadata, no user state); shared by REST and MCP.
// The frontend renders edit forms and review controls from it, and agents learn which fields each card kind has and which
// are required. The catalog is transitional: the note-type endpoint replaces it, and it is the only place the old kind
// vocabulary survives in this package.
func (a *API) CardTypes() CardTypesResponse {
	return CardTypesResponse{Kinds: cardKindCatalog()}
}

// cardTypes is GET /api/v1/card-types, scope: read.
func (a *API) cardTypes(c *gin.Context) {
	c.JSON(http.StatusOK, a.CardTypes())
}
