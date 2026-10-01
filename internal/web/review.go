package web

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/cardtype"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/schedule"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// 复习页的三个端点（M3-5）。评分与动作都返回主区域片段（htmx outerHTML swap），
// 响应里直接带下一张卡 —— 评分不产生额外交互往返（DESIGN.md §3.4、§8.2）。
const (
	reviewPagePath   = "/review"
	reviewAnswerPath = "/review/answer"
	reviewActionPath = "/review/action"
)

// registerReviewRoutes 挂载复习页。依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerReviewRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.cards == nil || s.presets == nil {
		return
	}
	router.GET(reviewPagePath, s.reviewPage)
	// 写操作一律过 CSRF 中间件（DESIGN.md §4.3、§11）。
	router.POST(reviewAnswerPath, s.sessions.CSRFMiddleware(), s.reviewAnswer)
	router.POST(reviewActionPath, s.sessions.CSRFMiddleware(), s.reviewAction)
}

// reviewPage 渲染整页：主区域（第一张卡）+ 键盘/滑动脚本。
func (s *Server) reviewPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID := parseUintQuery(c.Query("deck"))
	if deckID != 0 {
		if _, ok := s.loadOwnedDeck(c, user, deckID); !ok {
			return
		}
	}
	area, err := s.reviewArea(c, loc, user, deckID, 0, "")
	if err != nil {
		s.logger.Error("build review area failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	data := views.ReviewPageData{
		Layout: s.pageLayout(c, loc, "review.title"),
		JSURL:  s.assets.URL("js/review.js"),
		Area:   area,
	}
	renderHTML(c, views.ReviewPage(data))
}

// reviewAnswer 处理一次自评：单事务提交（schedule.Submit），随后在同一响应里返回下一张卡。
// 提交失败不静默：回滚后用局部化错误渲染当前卡，让用户可以重试。
func (s *Server) reviewAnswer(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if action := strings.TrimSpace(c.PostForm("action")); action != "" {
		s.reviewActionApply(c, loc, user, action)
		return
	}

	cardID := parseUintQuery(c.PostForm("card_id"))
	card, note, deck, ok := s.loadReviewCard(c, user, cardID)
	if !ok {
		return
	}
	rating, err := strconv.Atoi(strings.TrimSpace(c.PostForm("rating")))
	if err != nil || !schedule.Rating(rating).Valid() {
		area, aerr := s.reviewArea(c, loc, user, deck.ID, parseDone(c.PostForm("done")), loc.T("review.error_submit"))
		if aerr != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		renderReviewArea(c, area)
		return
	}
	expectedVersion := parseDone(c.PostForm("expected_version"))
	done := parseDone(c.PostForm("done"))
	elapsed := parseElapsed(c.PostForm("elapsed_ms"))

	sched, err := s.schedulerFor(c.Request.Context(), user.ID, note.DeckID)
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	ctx := c.Request.Context()
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "begin review transaction failed", tx.Error)
		return
	}
	if _, err := schedule.Submit(ctx, tx, schedule.SubmitInput{
		CardID:          card.ID,
		UserID:          user.ID,
		Rating:          schedule.Rating(rating),
		ExpectedVersion: expectedVersion,
		ElapsedMS:       elapsed,
		Scheduler:       sched,
		Now:             time.Now().UTC(),
	}); err != nil {
		_ = tx.Rollback().Error
		s.renderReviewError(c, loc, user, deck.ID, done, "submit review failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "commit review failed", err)
		return
	}

	area, err := s.reviewArea(c, loc, user, deck.ID, done+1, "")
	if err != nil {
		s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

// reviewAction 处理 u/s/b：撤销、暂停、埋藏。三者在同一事务里执行，随后同样返回下一张卡。
func (s *Server) reviewAction(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	action := strings.TrimSpace(c.PostForm("action"))
	s.reviewActionApply(c, loc, user, action)
}

// reviewActionApply 是 u/s/b 的公共实现。
func (s *Server) reviewActionApply(c *gin.Context, loc *i18n.Localizer, user *store.User, action string) {
	card, note, deck, ok := s.loadReviewCard(c, user, parseUintQuery(c.PostForm("card_id")))
	if !ok {
		return
	}
	done := parseDone(c.PostForm("done"))
	sched, err := s.schedulerFor(c.Request.Context(), user.ID, note.DeckID)
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "begin review action transaction failed", tx.Error)
		return
	}
	switch action {
	case "undo":
		_, err = schedule.Rollback(ctx, tx, schedule.UndoInput{
			CardID: card.ID, UserID: user.ID, Scheduler: sched, Now: now,
		})
	case "suspend":
		err = schedule.Suspend(ctx, tx, card.ID, now)
	case "bury":
		_, err = schedule.Bury(ctx, tx, schedule.BuryInput{
			CardID: card.ID, UserID: user.ID, Now: now,
		})
	default:
		_ = tx.Rollback().Error
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if err != nil {
		_ = tx.Rollback().Error
		s.renderReviewError(c, loc, user, deck.ID, done, "review action failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "commit review action failed", err)
		return
	}
	area, err := s.reviewArea(c, loc, user, deck.ID, done, "")
	if err != nil {
		s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

// renderReviewError 记录英文日志并回滚式地渲染当前卡 + 局部化错误提示，绝不静默。
func (s *Server) renderReviewError(c *gin.Context, loc *i18n.Localizer, user *store.User, deckID uint64, done int, logMsg string, err error) {
	s.logger.Error(logMsg, "user_id", user.ID, "error", err)
	area, aerr := s.reviewArea(c, loc, user, deckID, done, loc.T("review.error_submit"))
	if aerr != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

// renderReviewArea 只写出主区域片段（无页面外壳），供 htmx swap。
func renderReviewArea(c *gin.Context, area views.ReviewAreaData) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	if err := views.ReviewArea(area).Render(c.Request.Context(), c.Writer); err != nil {
		slog.Error("render review area failed", "error", err, "path", c.Request.URL.Path)
	}
}

// reviewArea 组装主区域：计数器、第一张待复习卡、评分与动作控件。
// 队列为空时返回局部化的“无可复习卡”提示；单卡渲染失败时降级为错误提示而不是整页 500。
func (s *Server) reviewArea(c *gin.Context, loc *i18n.Localizer, user *store.User, deckID uint64, done int, errText string) (views.ReviewAreaData, error) {
	sched, err := s.schedulerFor(c.Request.Context(), user.ID, deckID)
	if err != nil {
		return views.ReviewAreaData{}, err
	}
	items, err := s.buildQueue(c.Request.Context(), user.ID, deckID, sched)
	if err != nil {
		return views.ReviewAreaData{}, err
	}
	data := views.ReviewAreaData{
		RemainingLabel:   loc.T("review.remaining_label"),
		RemainingCount:   strconv.Itoa(len(items)),
		DoneLabel:        loc.T("review.done_label"),
		DoneCount:        strconv.Itoa(done),
		Empty:            len(items) == 0,
		EmptyText:        loc.T("review.empty"),
		ErrorText:        errText,
		NetworkErrorText: loc.T("review.error_network"),
		FrontLabel:       loc.T("review.front"),
		BackLabel:        loc.T("review.back"),
		ShowAnswerLabel:  loc.T("review.show_answer"),
		Ratings: []views.ReviewRating{
			{Value: int(schedule.Again), Label: loc.T("review.rating.again")},
			{Value: int(schedule.Hard), Label: loc.T("review.rating.hard")},
			{Value: int(schedule.Good), Label: loc.T("review.rating.good")},
			{Value: int(schedule.Easy), Label: loc.T("review.rating.easy")},
		},
		ShortcutsHint: loc.T("review.shortcuts"),
		UndoLabel:     loc.T("review.undo"),
		EditLabel:     loc.T("review.edit"),
		SuspendLabel:  loc.T("review.suspend"),
		BuryLabel:     loc.T("review.bury"),
		AnswerURL:     reviewAnswerPath,
		ActionURL:     reviewActionPath,
		DoneValue:     strconv.Itoa(done),
		ElapsedName:   "elapsed_ms",
	}
	if deckID != 0 {
		data.DeckValue = strconv.FormatUint(deckID, 10)
	}
	if sess, ok := auth.CurrentSession(c); ok {
		data.CSRF = sess.CSRFToken
	}
	if len(items) == 0 {
		return data, nil
	}
	card, err := s.cardView(c.Request.Context(), user, items[0])
	if err != nil {
		// 单张卡渲染失败（坏数据/未知题型）不应让整个复习流中断。
		s.logger.Error("render review card failed", "card_id", items[0].CardID, "error", err)
		data.ErrorText = loc.T("review.error_render")
		data.Empty = true
		data.EmptyText = loc.T("review.empty")
		return data, nil
	}
	data.Card = card
	data.HasCard = true
	data.CardEditHref = card.EditHref
	return data, nil
}

// buildQueue 复用 internal/schedule 的队列构建（每日上限、复习日切点、优先级都在那里）。
func (s *Server) buildQueue(ctx context.Context, userID, deckID uint64, sched *schedule.Scheduler) ([]schedule.QueueItem, error) {
	builder := schedule.NewQueueBuilder(s.db, sched)
	// 零值 QueueOptions 让每日上限走卡组设置（DeckID 非 0 时），否则回退文档化默认。
	return builder.Build(ctx, userID, schedule.QueueOptions{DeckID: deckID})
}

// schedulerFor 取卡组预设构造调度器；无卡组/预设缺失时回退到文档化默认参数。
func (s *Server) schedulerFor(ctx context.Context, userID, deckID uint64) (*schedule.Scheduler, error) {
	var preset *store.Preset
	if deckID != 0 {
		if deck, err := s.decks.ByID(ctx, deckID); err == nil && deck.PresetID != 0 {
			if p, err := s.presets.ByID(ctx, deck.PresetID); err == nil {
				preset = p
			}
		}
	}
	if preset == nil {
		def := store.NewPreset(userID, "default")
		preset = &def
	}
	return schedule.NewScheduler(preset)
}

// cardView 渲染一张卡的正反面并取乐观锁版本。
func (s *Server) cardView(ctx context.Context, user *store.User, item schedule.QueueItem) (*views.ReviewCardView, error) {
	card, err := s.cards.ByID(ctx, item.CardID)
	if err != nil {
		return nil, err
	}
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return nil, err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return nil, cardtype.UnknownKindError(note.Kind)
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return nil, err
	}
	ct := cardtype.Card{Template: card.Template, Ordinal: card.Ordinal, Fields: fields}
	front, err := t.Render(ct, cardtype.SideFront)
	if err != nil {
		return nil, err
	}
	back, err := t.Render(ct, cardtype.SideBack)
	if err != nil {
		return nil, err
	}
	frontHTML, err := renderSide(front)
	if err != nil {
		return nil, err
	}
	backHTML, err := renderSide(back)
	if err != nil {
		return nil, err
	}
	return &views.ReviewCardView{
		CardID:          strconv.FormatUint(card.ID, 10),
		NoteID:          strconv.FormatUint(note.ID, 10),
		DeckID:          strconv.FormatUint(note.DeckID, 10),
		ExpectedVersion: strconv.Itoa(s.stateVersion(ctx, user.ID, card.ID)),
		Template:        card.Template,
		FrontHTML:       frontHTML,
		BackHTML:        backHTML,
		EditHref:        "/decks/" + strconv.FormatUint(note.DeckID, 10) + "/notes/" + strconv.FormatUint(note.ID, 10),
	}, nil
}

// stateVersion 读取 (card_id, user_id) 的乐观锁版本；没有状态行的新卡返回 0（DESIGN.md §3.4）。
func (s *Server) stateVersion(ctx context.Context, userID, cardID uint64) int {
	var st store.CardState
	err := s.db.WithContext(ctx).Where("card_id = ? AND user_id = ?", cardID, userID).Take(&st).Error
	if err != nil {
		return 0
	}
	return st.Version
}

// loadReviewCard 取卡并校验它属于当前用户拥有的卡组；失败时写 404 并返回 false。
func (s *Server) loadReviewCard(c *gin.Context, user *store.User, cardID uint64) (*store.Card, *store.Note, *store.Deck, bool) {
	if cardID == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, nil, false
	}
	ctx := c.Request.Context()
	card, err := s.cards.ByID(ctx, cardID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, nil, false
	}
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, nil, false
	}
	deck, err := s.decks.ByID(ctx, note.DeckID)
	if err != nil || deck.OwnerUserID != user.ID {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, nil, false
	}
	return card, note, deck, true
}

// parseUintQuery 解析十进制无符号整数字符串；非法或空串返回 0。
func parseUintQuery(raw string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseDone 解析计数器；非法值按 0 处理（计数器只用于显示）。
func parseDone(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// parseElapsed 解析可选的答题耗时；缺失/非法时返回 nil（reviews.elapsed_ms 可空）。
func parseElapsed(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return nil
	}
	return &v
}
