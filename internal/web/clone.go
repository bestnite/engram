package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
)

// registerCloneRoutes 挂载卡组克隆（M5-4）。克隆对「自己可读」的卡组开放（reader 及以上），
// 把内容复制到调用者账号下；写操作过 CSRF 中间件。依赖未装配时跳过。
func (s *Server) registerCloneRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.presets == nil {
		return
	}
	router.POST("/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
	router.POST("/api/v1/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
}

// deckClone 把一个「自己可读」的卡组复制到当前账号下（M5-4）。
//
// 内容复制（note + card），进度不跟随（新 card 没有任何 card_states 行）；共享关系与可见性
// 不复制，克隆结果默认 private。预设按源预设参数复制一份到调用者名下，保证排程一致。
func (s *Server) deckClone(c *gin.Context) {
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
	// reader 及以上都能克隆：看到内容就能把它带走练自己的进度。
	src, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	presetID, err := s.clonePreset(ctx, loc, user.ID, src)
	if err != nil {
		s.logger.Error("clone preset for deck failed", "deck_id", src.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	name := loc.Tf("clone.name", map[string]any{"name": src.Name})
	cloned, err := s.decks.Clone(ctx, src, user.ID, name, presetID)
	if err != nil {
		s.logger.Error("clone deck failed", "deck_id", src.ID, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckClone,
		TargetType: "deck",
		TargetID:   store.Ptr(cloned.ID),
		Detail:     map[string]any{"source_deck_id": src.ID, "preset_id": cloned.PresetID},
	})
	if acceptsJSON(c) {
		c.JSON(http.StatusCreated, gin.H{"id": cloned.ID, "name": cloned.Name})
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/notes", cloned.ID))
}

// acceptsJSON 报告请求是否要求 JSON 响应（SPA 的 fetch 调用）。
//
// 只看 Accept 头里的 application/json 媒体类型，并允许它带参数或出现在候选列表里
// （如 "application/json; charset=utf-8" 或 "application/json, text/plain, */*"）：
// 用整串精确比较时，客户端一旦附上 charset 就会把 JSON 调用误判成表单提交，于是
// fetch 跟随 303 拿到 HTML，再按 JSON 解析失败。浏览器表单提交的 Accept 不含
// application/json，因此仍走 303 重定向。
func acceptsJSON(c *gin.Context) bool {
	for _, part := range strings.Split(c.GetHeader("Accept"), ",") {
		media := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if strings.EqualFold(media, "application/json") {
			return true
		}
	}
	return false
}

// clonePreset 复制一份源卡组的预设到调用者名下；源预设缺失时退回调用者的默认预设。
// 预设随克隆一起带走，保证克隆卡组的调度参数与源一致（同类语义）。
func (s *Server) clonePreset(ctx context.Context, loc *i18n.Localizer, ownerID uint64, src *store.Deck) (uint64, error) {
	srcPreset, err := s.presets.ByID(ctx, src.PresetID)
	if err != nil {
		// 源预设缺失（数据异常）时不让克隆整体失败：退回调用者的默认预设。
		return s.resolvePresetID(ctx, ownerID, "")
	}
	p := *srcPreset
	p.ID = 0
	p.OwnerUserID = ownerID
	p.Name = loc.Tf("clone.preset_name", map[string]any{"name": srcPreset.Name})
	p.CreatedAt = time.Time{}
	p.UpdatedAt = time.Time{}
	if err := s.presets.Create(ctx, &p); err != nil {
		return 0, err
	}
	return p.ID, nil
}
