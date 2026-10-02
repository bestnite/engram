package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
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
	summaries, err := s.decks.SummariesVisible(ctx, user.ID, time.Now().UTC())
	if err != nil {
		s.logger.Error("list decks failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presets, err := s.ensureDefaultPreset(ctx, loc, user.ID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderDeckList(c, loc, user.ID, summaries, presets, http.StatusOK, "", "", "")
}

// renderDeckList 渲染卡组列表页（含创建失败的回显分支）。
func (s *Server) renderDeckList(c *gin.Context, loc *i18n.Localizer, userID uint64, summaries []store.DeckSummary, presets []store.Preset, status int, errMsg, nameValue, descValue string) {
	rows := make([]views.DeckRow, 0, len(summaries))
	for i := range summaries {
		sm := summaries[i]
		// 共享管理页仅对 owner 开放（sharingPage 走 DeckAccess 判 owner）：只有本人拥有的行
		// 才给出入口，被共享给他人的行留空，避免非 owner 点进去吃 403（M5-2）。
		sharingHref := ""
		if sm.Deck.OwnerUserID == userID {
			sharingHref = fmt.Sprintf("/decks/%d/sharing", sm.Deck.ID)
		}
		rows = append(rows, views.DeckRow{
			IDValue:     strconv.FormatUint(sm.Deck.ID, 10),
			Name:        sm.Deck.Name,
			Href:        fmt.Sprintf("/decks/%d/notes", sm.Deck.ID),
			CardCount:   sm.CardCount,
			DueCount:    sm.DueCount,
			Archived:    sm.Deck.ArchivedAt != nil,
			ExportHref:  fmt.Sprintf("/decks/%d/package", sm.Deck.ID),
			SharingHref: sharingHref,
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
		Layout:      s.pageLayout(c, loc, "decks.list.title"),
		Heading:     loc.T("decks.list.heading"),
		ColName:     loc.T("decks.list.col_name"),
		ColCards:    loc.T("decks.list.col_cards"),
		ColDue:      loc.T("decks.list.col_due"),
		EmptyText:   loc.T("decks.list.empty"),
		Archived:    loc.T("decks.list.archived"),
		ColActions:  loc.T("decks.list.col_actions"),
		ExportLabel: loc.T("decks.list.export"),
		// M3-13：多卡组复习入口文案（每行「复习」、页头「复习所选」、复选框名称与提示）。
		ReviewLabel:         loc.T("decks.list.review"),
		ReviewSelectedLabel: loc.T("decks.list.review_selected"),
		SelectLabel:         loc.T("decks.list.select_label"),
		// M5-9：卡组包导入入口放在列表头部，不再往全局导航塞第六项。
		ImportLabel: loc.T("decks.list.import"),
		ImportHref:  "/import",
		// M5-2：owner 行的共享入口文案；非 owner 行的 SharingHref 为空，模板不渲染。
		SharingLabel: loc.T("decks.list.sharing"),
		Rows:         rows,
		NewHeading:   loc.T("decks.list.new_heading"),
		CloseLabel:   loc.T("common.close"),
		NameLabel:    loc.T("decks.list.name_label"),
		NameValue:    nameValue,
		DescLabel:    loc.T("decks.list.desc_label"),
		DescValue:    descValue,
		PresetLabel:  loc.T("decks.list.preset_label"),
		Presets:      options,
		CreateLabel:  loc.T("decks.list.create_submit"),
		ErrorMessage: errMsg,
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

	presetID, err := s.resolvePresetID(ctx, loc, user.ID, c.PostForm("preset_id"))
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
	summaries, err := s.decks.SummariesVisible(c.Request.Context(), userID, time.Now().UTC())
	if err != nil {
		s.logger.Error("list decks failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	presets, err := s.ensureDefaultPreset(c.Request.Context(), loc, userID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderDeckList(c, loc, userID, summaries, presets, status, errMsg, nameValue, descValue)
}

// ensureDefaultPreset 保证该用户至少有一个调度预设：没有就按文档化默认值建一个，幂等。
//
// 放在 web 渲染入口兜底，而不是改账号创建路径：本地注册、邀请注册、OIDC 首次登录等多条
// 路径都会建账号，逐条修改容易漏；在渲染卡组列表/表单与预设页时补齐只需一处，
// 且对已存在的历史账号同样生效（M3-14）。默认预设名走语言包，与卡组表单同源。
func (s *Server) ensureDefaultPreset(ctx context.Context, loc *i18n.Localizer, userID uint64) ([]store.Preset, error) {
	presets, err := s.presets.ListByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(presets) > 0 {
		return presets, nil
	}
	p := store.NewPreset(userID, loc.T("decks.preset.default"))
	if err := s.presets.Create(ctx, &p); err != nil {
		return nil, err
	}
	return []store.Preset{p}, nil
}

// resolvePresetID 解析表单里的 preset_id：必须属于当前用户；缺省或非法时退回第一个预设。
// 预设由 ensureDefaultPreset 保证至少有一个，因此不会出现「无预设可退回」的分支。
func (s *Server) resolvePresetID(ctx context.Context, loc *i18n.Localizer, userID uint64, raw string) (uint64, error) {
	presets, err := s.ensureDefaultPreset(ctx, loc, userID)
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
