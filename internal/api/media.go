package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// MediaItem 是媒体库列表的一项对外形态。
type MediaItem struct {
	Sha256    string    `json:"sha256"`
	Mime      string    `json:"mime"`
	Bytes     int64     `json:"bytes"`
	URL       string    `json:"url"`
	Width     *int      `json:"width,omitempty"`
	Height    *int      `json:"height,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MediaPage 是分页响应：items 为本页，next_cursor 为下一页游标（空串 = 到底）。
type MediaPage struct {
	Items      []MediaItem `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

// ListReadableMedia 返回该用户可读的媒体一页（REST `GET /api/v1/media`）。
//
// 业务规则集中在 store（可读谓词与 keyset 分页），这里只做参数校验、错误映射与形态转换：
//   - limit 必须落在 [1, MaxMediaPageSize]，否则 400（绝不静默截断成上限——调用方会以为
//     自己拿到了请求的条数）；
//   - 游标无法解码同样 400（静默从头开始会让翻页悄悄循环）；
//   - 返回空页时 Items 是空切片而非 null，保证 JSON 形状稳定。
func (a *API) ListReadableMedia(ctx context.Context, userID uint64, limit int, cursor string) (MediaPage, error) {
	if limit < 1 || limit > store.MaxMediaPageSize {
		return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("limit must be between 1 and %d", store.MaxMediaPageSize))
	}
	rows, next, err := store.ListReadableMedia(ctx, a.db, userID, limit, cursor)
	if err != nil {
		if errors.Is(err, store.ErrInvalidMediaCursor) {
			return MediaPage{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "cursor is invalid")
		}
		a.logger.Error("list readable media failed", "user_id", userID, "error", err)
		return MediaPage{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list media")
	}
	items := make([]MediaItem, 0, len(rows))
	for _, m := range rows {
		items = append(items, MediaItem{
			Sha256: m.Sha256,
			Mime:   m.Mime,
			Bytes:  m.Bytes,
			// URL 由哈希拼成，与上传响应的 url 同形：媒体选择器直接引用它，
			// 不在客户端重复拼接 /media/<sha256> 这一契约。
			URL:       "/media/" + m.Sha256,
			Width:     m.Width,
			Height:    m.Height,
			CreatedAt: m.CreatedAt,
		})
	}
	return MediaPage{Items: items, NextCursor: next}, nil
}

// listMedia 是 REST `GET /api/v1/media`：scope read，分页参数校验后交给 service。
func (a *API) listMedia(c *gin.Context) {
	u, _ := CurrentUser(c)
	limit := store.DefaultMediaPageSize
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("limit must be an integer between 1 and %d", store.MaxMediaPageSize))
			return
		}
		limit = v
	}
	page, err := a.ListReadableMedia(c.Request.Context(), u.ID, limit, strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
