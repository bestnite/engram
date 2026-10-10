package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// statsSummary 返回到期量 / 复习量 / 留存概要，scope: read（业务逻辑在 service 层的 Stats）。
func (a *API) statsSummary(c *gin.Context) {
	u, _ := CurrentUser(c)
	resp, err := a.Stats(c.Request.Context(), u)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
