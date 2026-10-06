package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 媒体选择器（媒体库）——编辑页上「从媒体库选择」入口与它的 htmx 片段端点。
//
// 逻辑不在这里：可读媒体集合与分页都是 service 层 api.API.ListReadableMedia（与 REST
// `GET /api/v1/media` 同一实现）。网页层只做角色判定、参数收窄与形态转换，保证 REST 与
// 网页看到的是同一份可读集合（AGENTS.md §2.3：一个业务层，两个传输）。

// mediaPickerData 构造编辑页上的选择器入口数据。媒体存储或 service 未装配时不渲染入口。
func (s *Server) mediaPickerData(loc *i18n.Localizer, deckID uint64) views.MediaPickerData {
	if s.media == nil || s.api == nil {
		return views.MediaPickerData{Disabled: true}
	}
	return views.MediaPickerData{
		OpenLabel: loc.T("media.picker.open"),
		URL:       fmt.Sprintf("/decks/%d/media/picker", deckID),
	}
}

// deckMediaPicker 返回选择器片段（htmx 局部替换用，不含整页外壳）。
//
// 需要卡组的 editor 及以上角色：选择器把引用插进卡片内容，reader 不能改内容（与
// 卡组内上传入口同一门槛）。片段只列当前用户可读的媒体——他人 private 卡组里的引用
// 不会出现（可读谓词与读取判定同源）。
func (s *Server) deckMediaPicker(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	if s.api == nil {
		// 装配缺失属于服务端配置问题，不能放行（与 loadDeckForRole 的缺失处理同口径）。
		s.logger.Error("media picker service is not wired", "deck_id", deckID)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 片段端点的 limit 是内部参数：非法的就退回默认，不返回错误页（它不是对外契约）。
	limit := store.DefaultMediaPageSize
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 1 && v <= store.MaxMediaPageSize {
			limit = v
		}
	}
	page, err := s.api.ListReadableMedia(c.Request.Context(), user.ID, limit, strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		// 非法游标由 service 映射成 400（ServiceError）；其余按 500。
		var se *api.ServiceError
		if errors.As(err, &se) {
			c.AbortWithStatus(se.Status)
			return
		}
		s.logger.Error("load media picker page failed", "deck_id", deckID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	data := views.MediaPickerFragmentData{
		Heading:   loc.T("media.picker.heading"),
		Empty:     loc.T("media.picker.empty"),
		NextLabel: loc.T("media.picker.next"),
		Items:     make([]views.MediaPickerItem, 0, len(page.Items)),
	}
	for _, item := range page.Items {
		src := "/media/" + item.Sha256
		data.Items = append(data.Items, views.MediaPickerItem{
			Sha256:    item.Sha256,
			Src:       src,
			InsertURL: src,
		})
	}
	if page.NextCursor != "" {
		data.NextURL = fmt.Sprintf("/decks/%d/media/picker?limit=%d&cursor=%s", deckID, limit, url.QueryEscape(page.NextCursor))
	}
	renderHTML(c, views.MediaPickerFragment(data))
}
