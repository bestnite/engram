package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// registerCloneRoutes 挂载卡组克隆。克隆对「自己可读」的卡组开放（reader 及以上），
// 把内容复制到调用者账号下；写操作过 CSRF 中间件。依赖未装配时跳过。
func (s *Server) registerCloneRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.presets == nil {
		return
	}
	router.POST("/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
	router.POST("/api/v1/decks/:id/clone", s.sessions.CSRFMiddleware(), s.deckClone)
}

// deckClone 把一个「自己可读」的卡组复制到当前账号下。
//
// 内容复制（note + card），进度不跟随（新 card 没有任何 card_states 行）；共享授权不复制，
// 克隆结果只有调用者一个 owner。克隆卡组沿用调用者自己在源卡组上生效的预设：克隆别人的卡组
// 不会把属主的调度参数（含优化出的权重）复制给调用者，学习参数始终是各人自己的。
func (s *Server) deckClone(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	// reader 及以上都能克隆：看到内容就能把它带走练自己的进度。
	src, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	settings, err := s.decks.StudySettings(ctx, user.ID, src)
	if err != nil {
		s.logger.Error("load study settings for deck clone failed", "deck_id", src.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presetID := settings.PresetID
	// 副本名 = 源名 + 本地化后缀；由 store.DeckCopyName 按 rune 截断源名，
	// 保证接近上限的合法源名不会产出超长副本名。
	suffix := loc.Tf("clone.name_suffix", nil)
	name := store.DeckCopyName(src.Name, suffix)
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
		Detail:     map[string]any{"source_deck_id": src.PublicID, "preset_id": cloned.PresetID},
	})
	if acceptsJSON(c) {
		c.JSON(http.StatusCreated, gin.H{"id": cloned.PublicID, "name": cloned.Name})
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%s/notes", cloned.PublicID))
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
