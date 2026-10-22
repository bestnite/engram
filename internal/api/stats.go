package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// statsSummary returns the due / review / retention summary, scope: read (the logic lives in the service layer's Stats).
func (a *API) statsSummary(c *gin.Context) {
	u, _ := CurrentUser(c)
	resp, err := a.Stats(c.Request.Context(), u)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
