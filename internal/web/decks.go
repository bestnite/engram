package web

import (
	"context"
	"errors"
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

// registerDeckRoutes 挂载卡组列表与新建页（M2-11）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerDeckRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.presets == nil {
		return
	}
	router.GET("/decks", s.deckList)
	// 写操作过 CSRF 中间件（DESIGN.md §4.3、§11）。
	router.POST("/decks", s.sessions.CSRFMiddleware(), s.deckCreate)
}

// deckList 渲染当前用户的卡组列表与新建表单；匿名访问被重定向到登录页。
func (s *Server) deckList(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	summaries, err := s.decks.SummariesVisible(ctx, user.ID)
	if err != nil {
		s.logger.Error("list decks failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	counts, err := s.deckQueueCounts(ctx, user.ID, summaries)
	if err != nil {
		s.logger.Error("count deck queue failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presets, err := store.EnsureDefaultPreset(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderDeckList(c, loc, user.ID, summaries, counts, presets, http.StatusOK, "", "", "")
}

// deckQueueCounts 取列表页每个卡组「今日可刷」的构成（新 / 复习两个数），走 schedule.DeckCounts：
// 它与 /review 的取卡路径同源，因此两个数相加＝点进去实际能刷的张数。
// sched 用默认预设（schedulerFor 传 nil）——数量统计不算 retrievability，预设不影响结果。
func (s *Server) deckQueueCounts(ctx context.Context, userID uint64, summaries []store.DeckSummary) (map[uint64]schedule.DeckQueueCounts, error) {
	ids := make([]uint64, 0, len(summaries))
	for i := range summaries {
		ids = append(ids, summaries[i].Deck.ID)
	}
	sched, err := s.schedulerFor(ctx, userID, nil)
	if err != nil {
		return nil, err
	}
	return schedule.NewQueueBuilder(s.db, s.decks, sched).DeckCounts(ctx, userID, ids)
}

// renderDeckList 渲染卡组列表页（含创建失败的回显分支）。counts 是各卡组今日可刷的构成。
func (s *Server) renderDeckList(c *gin.Context, loc *i18n.Localizer, userID uint64, summaries []store.DeckSummary, counts map[uint64]schedule.DeckQueueCounts, presets []store.Preset, status int, errMsg, nameValue, descValue string) {
	rows := make([]views.DeckRow, 0, len(summaries))
	for i := range summaries {
		sm := summaries[i]
		// 共享/设置管理页仅对 owner 开放：只有本人拥有的行才给出入口，被共享给他人的行留空，
		// 避免非 owner 点进去吃 403（M5-2）。
		sharingHref := ""
		settingsHref := ""
		if sm.Deck.OwnerUserID == userID {
			sharingHref = fmt.Sprintf("/decks/%d/sharing", sm.Deck.ID)
			settingsHref = fmt.Sprintf("/decks/%d/settings", sm.Deck.ID)
		}
		// 今日可刷的两个数来自 schedule.DeckCounts，与 /review 同源——两者相加＝点进去能刷的张数。
		queue := counts[sm.Deck.ID]
		rows = append(rows, views.DeckRow{
			IDValue:      strconv.FormatUint(sm.Deck.ID, 10),
			Name:         sm.Deck.Name,
			Href:         fmt.Sprintf("/decks/%d/notes", sm.Deck.ID),
			CardCount:    sm.CardCount,
			NewCount:     int64(queue.New),
			ReviewCount:  int64(queue.Review),
			Archived:     sm.Deck.ArchivedAt != nil,
			ExportHref:   fmt.Sprintf("/decks/%d/package", sm.Deck.ID),
			SharingHref:  sharingHref,
			SettingsHref: settingsHref,
			// M3-13：只复习该卡组的入口，与列表页勾选后提交的 /review?deck=... 同一口径。
			ReviewHref: fmt.Sprintf("/review?deck=%d", sm.Deck.ID),
		})
	}
	options := make([]views.DeckOption, 0, len(presets))
	for i := range presets {
		options = append(options, views.DeckOption{
			Value:    strconv.FormatUint(presets[i].ID, 10),
			Label:    presets[i].Name,
			Selected: i == 0,
		})
	}
	data := views.DeckListData{
		Layout:   s.pageLayout(c, loc, "decks.list.title"),
		Heading:  loc.T("decks.list.heading"),
		ColName:  loc.T("decks.list.col_name"),
		ColCards: loc.T("decks.list.col_cards"),
		ColDue:   loc.T("decks.list.col_due"),
		// 今日列的两个数字的标签（新 X · 复习 Y）。
		NewCardsLabel:    loc.T("decks.list.new_cards"),
		ReviewCardsLabel: loc.T("decks.list.review_cards"),
		EmptyText:        loc.T("decks.list.empty"),
		Archived:         loc.T("decks.list.archived"),
		ColActions:       loc.T("decks.list.col_actions"),
		ExportLabel:      loc.T("decks.list.export"),
		// M3-13：多卡组复习入口文案（每行「复习」、页头「复习所选」、复选框名称与提示）。
		ReviewLabel:         loc.T("decks.list.review"),
		ReviewSelectedLabel: loc.T("decks.list.review_selected"),
		SelectLabel:         loc.T("decks.list.select_label"),
		// M5-9：卡组包导入入口放在列表头部，不再往全局导航塞第六项。
		ImportLabel: loc.T("decks.list.import"),
		ImportHref:  "/import",
		// M5-2：owner 行的共享入口文案；非 owner 行的 SharingHref 为空，模板不渲染。
		SharingLabel: loc.T("decks.list.sharing"),
		// owner 行的设置入口文案；非 owner 行的 SettingsHref 为空，模板不渲染。
		SettingsLabel: loc.T("decks.list.settings"),
		Rows:          rows,
		NewHeading:    loc.T("decks.list.new_heading"),
		CloseLabel:    loc.T("common.close"),
		NameLabel:     loc.T("decks.list.name_label"),
		NameValue:     nameValue,
		DescLabel:     loc.T("decks.list.desc_label"),
		DescValue:     descValue,
		PresetLabel:   loc.T("decks.list.preset_label"),
		Presets:       options,
		CreateLabel:   loc.T("decks.list.create_submit"),
		ErrorMessage:  errMsg,
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.DeckListPage(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// deckCreate 处理新建卡组表单：校验名称、解析预设（无预设时创建默认预设）、落库并写审计。
func (s *Server) deckCreate(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	if name == "" {
		s.renderDeckCreateError(c, loc, user.ID, http.StatusBadRequest, loc.T("decks.list.error_name_required"), name, description)
		return
	}

	presetID, err := s.resolvePresetID(ctx, user.ID, c.PostForm("preset_id"))
	if err != nil {
		s.logger.Error("resolve preset for new deck failed", "user_id", user.ID, "error", err)
		s.renderDeckCreateError(c, loc, user.ID, http.StatusInternalServerError, loc.T("decks.list.error_create_failed"), name, description)
		return
	}

	deck := &store.Deck{
		OwnerUserID: user.ID,
		Name:        name,
		Description: description,
		Visibility:  store.DeckVisibilityPrivate,
		PresetID:    presetID,
	}
	if err := s.decks.Create(ctx, deck); err != nil {
		s.logger.Info("create deck rejected", "user_id", user.ID, "error", err)
		// 卡组名不满足与卡组包 manifest 同源的规则时给专属提示，其余失败仍是通用文案。
		if errors.Is(err, store.ErrDeckNameInvalid) {
			s.renderDeckCreateError(c, loc, user.ID, http.StatusBadRequest, loc.T("decks.list.error_name_invalid"), name, description)
			return
		}
		s.renderDeckCreateError(c, loc, user.ID, http.StatusBadRequest, loc.T("decks.list.error_create_failed"), name, description)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionDeckCreate,
		TargetType: "deck",
		TargetID:   store.Ptr(deck.ID),
		Detail:     map[string]any{"name": deck.Name, "preset_id": deck.PresetID},
	})
	c.Redirect(http.StatusSeeOther, "/decks")
}

// renderDeckCreateError 在创建失败时重新渲染列表页并带上本地化错误与回显值。
func (s *Server) renderDeckCreateError(c *gin.Context, loc *i18n.Localizer, userID uint64, status int, errMsg, nameValue, descValue string) {
	ctx := c.Request.Context()
	summaries, err := s.decks.SummariesVisible(ctx, userID)
	if err != nil {
		s.logger.Error("list decks failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	counts, err := s.deckQueueCounts(ctx, userID, summaries)
	if err != nil {
		s.logger.Error("count deck queue failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderDeckList(c, loc, userID, summaries, counts, presets, status, errMsg, nameValue, descValue)
}

// resolvePresetID 解析表单里的 preset_id：必须属于当前用户；缺省或非法时退回第一个预设。
// 预设由 store.EnsureDefaultPreset 保证至少有一个，因此不会出现「无预设可退回」的分支。
func (s *Server) resolvePresetID(ctx context.Context, userID uint64, raw string) (uint64, error) {
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		return 0, err
	}
	want, _ := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	for i := range presets {
		if presets[i].ID == want {
			return presets[i].ID, nil
		}
	}
	return presets[0].ID, nil
}
