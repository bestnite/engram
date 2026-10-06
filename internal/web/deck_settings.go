package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerDeckSettingsRoutes 挂载卡组设置页（仅 owner）：改每日新卡 / 复习上限。
//
// 门控与 registerDeckRoutes 相同：列表页上渲染的 /decks/:id/settings 链接与这里注册的路由
// 必须共享同一依赖前提，否则会出现「链接在、点进去 404」——这正是本次要补齐的缺口。
// 写操作过 CSRF 中间件（DESIGN.md §4.3、§11）。
func (s *Server) registerDeckSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/decks/:id/settings", s.deckSettingsRoute)
	router.POST("/decks/:id/settings", s.sessions.CSRFMiddleware(), s.deckSettingsUpdate)
}

// deckSettingsPage 渲染卡组设置页；只有 owner 能打开（非 owner 由 loadDeckForRole 写 403）。
func (s *Server) deckSettingsPage(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}
	s.renderDeckSettings(c, loc, user, deck, http.StatusOK, "", s.deckSettingsNotice(loc, c.Query("notice")))
}

// deckSettingsNotice 把重定向回带的 notice 码翻成文案；未知码不显示（与 admin_keys / admin_jobs 同形）。
func (s *Server) deckSettingsNotice(loc *i18n.Localizer, code string) string {
	if code == "saved" {
		return loc.T("decks.settings.notice.saved")
	}
	return ""
}

// deckSettingsUpdate 保存每日上限（仅 owner）：0 合法（不限），负 / 非数字 / 空一律拒绝且不写库。
func (s *Server) deckSettingsUpdate(c *gin.Context) {
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
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner)
	if !ok {
		return
	}

	newPer, okNew := parseDeckCap(c.PostForm("new_per_day"))
	reviewPer, okReview := parseDeckCap(c.PostForm("reviews_per_day"))
	if !okNew || !okReview {
		// 非法输入：不写库，就地渲染 400 并给局部化错误文案。
		s.renderDeckSettings(c, loc, user, deck, http.StatusBadRequest, loc.T("decks.settings.error.invalid_number"), "")
		return
	}

	caps := store.DeckCaps{NewPerDay: newPer, ReviewsPerDay: reviewPer}
	// 权限断言在 store.SetCaps 的 mutateOwned 里，handler 不再另写一套；
	// SetCaps 用 map 更新，0 会原样落库（不受 GORM 对带默认值列的零值省略影响）。
	if err := s.decks.SetCaps(c.Request.Context(), user.ID, deck.ID, caps); err != nil {
		// 走到这里只可能是校验/落库失败（权限已由 loadDeckForRole 挡下）。
		s.logger.Error("set deck caps failed", "deck_id", deck.ID, "error", err)
		s.renderDeckSettings(c, loc, user, deck, http.StatusInternalServerError, loc.T("decks.settings.error.save_failed"), "")
		return
	}
	// 与其他卡组变更（创建/克隆/授权/可见性）同一口径：改额度也要留痕，
	// 它决定了这个卡组每天向所有使用者放多少张卡出来（DESIGN.md §11）。
	s.audit(c.Request.Context(), store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckCaps,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"new_per_day": caps.NewPerDay, "reviews_per_day": caps.ReviewsPerDay},
	})
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("/decks/%d/settings?notice=saved", deck.ID))
}

// parseDeckCap 解析每日上限输入：空串、非数字、负数都算非法；0 是合法值（不限）。
// 拒绝发生在 handler 层是为了不回显脏值、也不把负值交给 store（store 也会再校验一次）。
func parseDeckCap(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// deckBudget 取单个卡组今日的额度情况，走 schedule.DeckBudgets（与复习队列同源）。
func (s *Server) deckBudget(ctx context.Context, userID, deckID uint64) (schedule.DeckBudget, error) {
	sched, err := s.schedulerFor(ctx, userID, []uint64{deckID})
	if err != nil {
		return schedule.DeckBudget{}, err
	}
	budgets, err := schedule.NewQueueBuilder(s.db, s.decks, sched).DeckBudgets(ctx, userID, []uint64{deckID})
	if err != nil {
		return schedule.DeckBudget{}, err
	}
	return budgets[deckID], nil
}

// renderDeckSettings 装配卡组设置页数据并写出；status/errMsg 用于把校验失败渲染成 4xx。
// 当前上限与今日已用、剩余都取自 deckBudget，网页层不重算额度公式。
func (s *Server) renderDeckSettings(c *gin.Context, loc *i18n.Localizer, user *store.User, deck *store.Deck, status int, errMsg, notice string) {
	ctx := c.Request.Context()
	budget, err := s.deckBudget(ctx, user.ID, deck.ID)
	if err != nil {
		s.logger.Error("load deck budget failed", "deck_id", deck.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	data := views.DeckSettingsData{
		Layout:       s.pageLayout(c, loc, "decks.settings.title"),
		Heading:      loc.T("decks.settings.heading"),
		DeckName:     deck.Name,
		BackLabel:    loc.T("decks.settings.back"),
		BackHref:     "/decks",
		Notice:       notice,
		ErrorMessage: errMsg,
		Action:       fmt.Sprintf("/decks/%d/settings", deck.ID),
		// 表单初值取库里真实的列值：0 原样显示为 0，不是「不限」之外的其它数。
		NewPerDayValue:     strconv.Itoa(budget.NewPerDay),
		ReviewsPerDayValue: strconv.Itoa(budget.ReviewsPerDay),
		NewPerDayLabel:     loc.T("decks.settings.new_per_day_label"),
		ReviewsPerDayLabel: loc.T("decks.settings.reviews_per_day_label"),
		UnlimitedHint:      loc.T("decks.settings.unlimited_hint"),
		UsageHeading:       loc.T("decks.settings.usage_heading"),
		NewUsedLabel:       loc.T("decks.settings.new_used_label"),
		NewLeftLabel:       loc.T("decks.settings.new_left_label"),
		ReviewUsedLabel:    loc.T("decks.settings.review_used_label"),
		ReviewLeftLabel:    loc.T("decks.settings.review_left_label"),
		UnlimitedValue:     loc.T("decks.settings.unlimited_value"),
		SubmitLabel:        loc.T("decks.settings.submit"),
	}
	data.NewUsedValue = strconv.Itoa(budget.NewUsed)
	data.ReviewUsedValue = strconv.Itoa(budget.ReviewUsed)
	data.NewLeftValue = deckLeftText(budget.NewLeft, budget.NewUnlimited, data.UnlimitedValue)
	data.ReviewLeftValue = deckLeftText(budget.ReviewLeft, budget.ReviewUnlimited, data.UnlimitedValue)

	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.DeckSettingsPage(data).Render(ctx, c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// deckLeftText 把「剩余」渲染成文本：不限时显示不限文案（不是 0 冒充不限），否则显示数字。
func deckLeftText(left int, unlimited bool, unlimitedText string) string {
	if unlimited {
		return unlimitedText
	}
	return strconv.Itoa(left)
}

// deckSettingsRoute 提供 GET /decks/:id/settings：SPA 已加载时返回应用壳（DESIGN.md §8.1、§8.5），
// 由客户端路由渲染卡组每日上限页；读写走 /api/v1/decks/:id/settings（同一份服务逻辑与审计）。
//
// owner 门禁留在服务端：非 owner 与不存在的卡组在返回应用壳之前就以 403 / 404 结束，
// 与迁移前的 SSR 页面完全一致（否则会让无权用户拿到页面外壳）。SPA 缺失时回退 SSR 页面。
func (s *Server) deckSettingsRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleOwner); !ok {
		return
	}
	if s.spa != nil {
		s.spa.ServeIndex(c)
		return
	}
	s.deckSettingsPage(c)
}
