package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
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

// countNonAgain 统计该用户 rating != 1 的复习条数，作为留存率分子。
func countNonAgain(ctx context.Context, a *API, userID uint64) (int64, error) {
	var n int64
	err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND rating > 1", userID).Count(&n).Error
	return n, err
}
