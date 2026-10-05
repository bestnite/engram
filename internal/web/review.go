package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
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

// reviewScope 是一次复习请求的卡组范围：deckIDs 为空表示全库（不按卡组过滤）。
// 范围由 URL 的可重复 deck 参数决定，并由每个评分/动作表单原样带回（DESIGN.md §8.2）。
type reviewScope struct{ deckIDs []uint64 }

// deckScopeFromValues 解析可重复的 deck 参数值：去重、跳过空串；任一值非数字或为 0
// 即写 400 并返回 false。随后逐个校验 loadDeckForRole(..., RoleReader)：任何缺失或
// 无权限的卡组都让整次请求失败（404/403，由该 helper 写出），绝不静默丢弃某个卡组。
func (s *Server) deckScopeFromValues(c *gin.Context, user *store.User, raw []string) (reviewScope, bool) {
	ids := make([]uint64, 0, len(raw))
	seen := make(map[uint64]bool, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			c.AbortWithStatus(http.StatusBadRequest)
			return reviewScope{}, false
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range ids {
		if _, ok := s.loadDeckForRole(c, user, id, store.RoleReader); !ok {
			return reviewScope{}, false
		}
	}
	return reviewScope{deckIDs: ids}, true
}

// parseDeckScope 解析 GET 查询串里的可重复 deck 参数（复习页整页请求）。
func (s *Server) parseDeckScope(c *gin.Context, user *store.User) (reviewScope, bool) {
	return s.deckScopeFromValues(c, user, c.QueryArray("deck"))
}

// parseDeckScopeForm 解析表单里的可重复 deck 参数（评分/动作请求原样带回的范围）。
func (s *Server) parseDeckScopeForm(c *gin.Context, user *store.User) (reviewScope, bool) {
	return s.deckScopeFromValues(c, user, c.PostFormArray("deck"))
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
	scope, ok := s.parseDeckScope(c, user)
	if !ok {
		return
	}
	area, err := s.reviewArea(c, loc, user, scope, 0, "")
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
	scope, ok := s.parseDeckScopeForm(c, user)
	if !ok {
		return
	}
	if action := strings.TrimSpace(c.PostForm("action")); action != "" {
		s.reviewActionApply(c, loc, user, scope, action)
		return
	}

	cardID := parseUintQuery(c.PostForm("card_id"))
	card, note, _, ok := s.loadReviewCard(c, user, cardID, store.RoleReader)
	if !ok {
		return
	}
	// 作答类题型（M3-12）：题型实现 Grader 时走机器判分，不再依赖四档自评。
	if _, graded := graderFor(note.Kind); graded {
		// 已揭示答案再作答等于抄答案：不再判分，直接记 Again（DESIGN.md §8.2）。
		// 页面上揭示后输入框与提交按钮会被禁用，只留一个「记 0 分并继续」按钮带 revealed=1。
		if c.PostForm("revealed") != "" {
			s.reviewRevealedAnswer(c, loc, user, scope, card, note)
			return
		}
		s.reviewGradedAnswer(c, loc, user, scope, card, note)
		return
	}
	rating, err := strconv.Atoi(strings.TrimSpace(c.PostForm("rating")))
	if err != nil || !schedule.Rating(rating).Valid() {
		area, aerr := s.reviewArea(c, loc, user, scope, parseDone(c.PostForm("done")), loc.T("review.error_submit"))
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

	// 提交始终用被评卡所属卡组的预设调度；范围只影响队列构建（DESIGN.md §3.3）。
	sched, err := s.schedulerFor(c.Request.Context(), user.ID, []uint64{note.DeckID})
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	ctx := c.Request.Context()
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, scope, done, "begin review transaction failed", tx.Error)
		return
	}
	if _, err := schedule.Submit(ctx, tx, schedule.SubmitInput{
		CardID:          card.ID,
		UserID:          user.ID,
		Rating:          schedule.Rating(rating),
		ExpectedVersion: expectedVersion,
		ElapsedMS:       elapsed,
		Location:        userLocation(user),
		DayCutoffHour:   user.DayCutoffHour,
		Scheduler:       sched,
		Now:             time.Now().UTC(),
	}); err != nil {
		_ = tx.Rollback().Error
		s.renderReviewError(c, loc, user, scope, done, "submit review failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, scope, done, "commit review failed", err)
		return
	}

	area, err := s.reviewArea(c, loc, user, scope, done+1, "")
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
	scope, ok := s.parseDeckScopeForm(c, user)
	if !ok {
		return
	}
	action := strings.TrimSpace(c.PostForm("action"))
	s.reviewActionApply(c, loc, user, scope, action)
}

// reviewActionApply 是 u/s/b 的公共实现。
func (s *Server) reviewActionApply(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, action string) {
	// suspend 写的是 cards.suspended_at —— 卡片级、对所有使用者生效的状态（DESIGN.md §3.4），
	// 必须 owner-only；其余动作只影响本人数据（undo 删自己的 reviews 行、bury 写自己的
	// card_states），共享卡组的 reader 即可（DESIGN.md §5“reader 只能自己复习”）。
	want := store.RoleReader
	if action == "suspend" {
		want = store.RoleOwner
	}
	card, note, _, ok := s.loadReviewCard(c, user, parseUintQuery(c.PostForm("card_id")), want)
	if !ok {
		return
	}
	done := parseDone(c.PostForm("done"))
	// “继续”只是换下一张卡：判分已经在提交那一步写入，无需再开事务（M3-12）。
	if action == "next" {
		area, err := s.reviewArea(c, loc, user, scope, done, "")
		if err != nil {
			s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		renderReviewArea(c, area)
		return
	}
	// 动作始终用被操作卡所属卡组的预设调度；范围只影响队列构建（DESIGN.md §3.3）。
	sched, err := s.schedulerFor(c.Request.Context(), user.ID, []uint64{note.DeckID})
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	ctx := c.Request.Context()
	now := time.Now().UTC()
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, scope, done, "begin review action transaction failed", tx.Error)
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
		s.renderReviewError(c, loc, user, scope, done, "review action failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, scope, done, "commit review action failed", err)
		return
	}
	area, err := s.reviewArea(c, loc, user, scope, done, "")
	if err != nil {
		s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

// renderReviewError 记录英文日志并回滚式地渲染当前卡 + 局部化错误提示，绝不静默。
func (s *Server) renderReviewError(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, done int, logMsg string, err error) {
	s.logger.Error(logMsg, "user_id", user.ID, "error", err)
	area, aerr := s.reviewArea(c, loc, user, scope, done, loc.T("review.error_submit"))
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
func (s *Server) reviewArea(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, done int, errText string) (views.ReviewAreaData, error) {
	sched, err := s.schedulerFor(c.Request.Context(), user.ID, scope.deckIDs)
	if err != nil {
		return views.ReviewAreaData{}, err
	}
	items, err := s.buildQueue(c.Request.Context(), user.ID, scope, sched)
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
		ZeroScoreLabel:   loc.T("review.zero_score"),
		ShortcutsHint:    loc.T("review.shortcuts"),
		BuryHint:         loc.T("review.bury_hint"),
		EditLabel:        loc.T("review.edit"),
		BuryLabel:        loc.T("review.bury"),
		AnswerURL:        reviewAnswerPath,
		ActionURL:        reviewActionPath,
		DoneValue:        strconv.Itoa(done),
		ElapsedName:      "elapsed_ms",
	}
	// 每个选中卡组渲染一个隐藏 deck 字段，评分/动作请求据此原样带回同一范围（DESIGN.md §8.2）。
	if len(scope.deckIDs) > 0 {
		data.DeckValues = make([]string, 0, len(scope.deckIDs))
		for _, id := range scope.deckIDs {
			data.DeckValues = append(data.DeckValues, strconv.FormatUint(id, 10))
		}
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
	// 作答类题型渲染输入控件代替四档自评（M3-12）。
	data.Graded = s.gradedViewForCard(c.Request.Context(), loc, items[0])
	// 自评类题型才有四档按钮。每档附上"这张卡下次什么时候回来"（DESIGN.md §8.2）：
	// 学习步骤以分钟计，不显示的话"刚答完几分钟后又冒出来"只能靠猜。
	if data.Graded == nil {
		data.Ratings = s.reviewRatings(c.Request.Context(), loc, sched, user, items[0], time.Now().UTC())
	} else {
		// 判分卡没有 1–4 档位，提示串不能继续宣传"1–4：评分"——那几个键在这里不响。
		data.ShortcutsHint = loc.T("review.shortcuts_graded")
	}
	return data, nil
}

// reviewRatings 构造四档评分按钮，并用调度器预览给每档附上一句等待时长。
// 预览失败（坏数据、未知状态）只丢弃时长，绝不能因此让复习页打不开。
func (s *Server) reviewRatings(ctx context.Context, loc *i18n.Localizer, sched *schedule.Scheduler, user *store.User, item schedule.QueueItem, now time.Time) []views.ReviewRating {
	ratings := []views.ReviewRating{
		{Value: int(schedule.Again), Label: loc.T("review.rating.again")},
		{Value: int(schedule.Hard), Label: loc.T("review.rating.hard")},
		{Value: int(schedule.Good), Label: loc.T("review.rating.good")},
		{Value: int(schedule.Easy), Label: loc.T("review.rating.easy")},
	}
	st, err := s.cardStateForPreview(ctx, user.ID, item)
	if err != nil {
		s.logger.Error("load card state for preview failed", "card_id", item.CardID, "error", err)
		return ratings
	}
	outcomes, err := sched.Preview(st, now)
	if err != nil {
		s.logger.Error("preview ratings failed", "card_id", item.CardID, "error", err)
		return ratings
	}
	// Preview 的返回顺序固定为 Again/Hard/Good/Easy，与上面构造顺序一致。
	for i := range outcomes {
		if i >= len(ratings) {
			break
		}
		ratings[i].Interval = intervalLabel(loc, outcomes[i].Due.Sub(now))
	}
	return ratings
}

// cardStateForPreview 取当前卡的调度状态行；全新卡没有状态行，按 new 合成一行供预览用
// （首次评分的预览正是用户最容易困惑的那次——答完 1 分钟就回来）。
func (s *Server) cardStateForPreview(ctx context.Context, userID uint64, item schedule.QueueItem) (*store.CardState, error) {
	var st store.CardState
	err := s.db.WithContext(ctx).Where("card_id = ? AND user_id = ?", item.CardID, userID).First(&st).Error
	switch {
	case err == nil:
		return &st, nil
	case store.IsNotFound(err):
		return &store.CardState{CardID: item.CardID, UserID: userID, State: item.State.String()}, nil
	default:
		return nil, fmt.Errorf("load card state for card %d user %d: %w", item.CardID, userID, err)
	}
}

// buildQueue 复用 internal/schedule 的队列构建（每日上限、复习日切点、优先级都在那里）。
// 单卡组走 DeckID 以保留卡组上限口径；多卡组走 DeckIDs 集合（合并后统一排序/打乱）。
func (s *Server) buildQueue(ctx context.Context, userID uint64, scope reviewScope, sched *schedule.Scheduler) ([]schedule.QueueItem, error) {
	builder := schedule.NewQueueBuilder(s.db, s.decks, sched)
	opts := schedule.QueueOptions{}
	switch len(scope.deckIDs) {
	case 0:
	case 1:
		opts.DeckID = scope.deckIDs[0]
	default:
		opts.DeckIDs = scope.deckIDs
	}
	return builder.Build(ctx, userID, opts)
}

// schedulerFor 取卡组预设构造调度器；只有恰好指定一个卡组时才用它的预设，
// 多卡组与全库回退到文档化默认参数（DESIGN.md §3.3）。无卡组/预设缺失同样回退默认。
func (s *Server) schedulerFor(ctx context.Context, userID uint64, deckIDs []uint64) (*schedule.Scheduler, error) {
	var preset *store.Preset
	if len(deckIDs) == 1 {
		if deck, err := s.decks.ByID(ctx, deckIDs[0]); err == nil && deck.PresetID != 0 {
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

// loadReviewCard 取卡并校验当前用户对卡所属卡组至少拥有 want 角色；失败时写 403/404 并返回 false。
// 判权委托给 loadDeckForRole（与 REST/MCP 共用 auth.DeckAccess，不复制第二份判定）。
// 复习的进度键是 (card_id, user_id)，写的是本人数据，因此评分/undo/bury/next 传 store.RoleReader，
// 共享卡组的读者也能复习；suspend 例外（见 reviewActionApply），调用方必须传 store.RoleOwner。
func (s *Server) loadReviewCard(c *gin.Context, user *store.User, cardID uint64, want string) (*store.Card, *store.Note, *store.Deck, bool) {
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
	deck, ok := s.loadDeckForRole(c, user, note.DeckID, want)
	if !ok {
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

// graderFor 用可选窄接口断言判断题型是否支持机器判分（DESIGN.md §6.2、M3-12）。
// 判分能力是可选能力，核心管线不依赖它：未实现 Grader 的题型返回 false，仍走四档自评。
func graderFor(kind string) (cardtype.Grader, bool) {
	t, ok := cardtype.Lookup(kind)
	if !ok {
		return nil, false
	}
	g, ok := t.(cardtype.Grader)
	return g, ok
}

// gradeMappingFor 取卡组预设的「分数→评分档位」映射；缺失时返回 nil（判分器回退默认映射）。
func (s *Server) gradeMappingFor(ctx context.Context, deckID uint64) *cardtype.GradeMapping {
	if deckID == 0 {
		return nil
	}
	deck, err := s.decks.ByID(ctx, deckID)
	if err != nil || deck.PresetID == 0 {
		return nil
	}
	p, err := s.presets.ByID(ctx, deck.PresetID)
	if err != nil {
		return nil
	}
	m, err := p.GradeMapping()
	if err != nil {
		return nil
	}
	return &m
}

// gradedViewForCard 判断队列头的卡是否为作答类题型，是则构造输入控件数据；否则返回 nil。
func (s *Server) gradedViewForCard(ctx context.Context, loc *i18n.Localizer, item schedule.QueueItem) *views.ReviewGradedView {
	card, err := s.cards.ByID(ctx, item.CardID)
	if err != nil {
		return nil
	}
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return nil
	}
	if _, ok := graderFor(note.Kind); !ok {
		return nil
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return nil
	}
	return buildGradedView(loc, note.Kind, fields)
}

// buildGradedView 按题型把 note 字段转成前端控件描述；未知题型返回 nil（回退自评）。
func buildGradedView(loc *i18n.Localizer, kind string, fields map[string]any) *views.ReviewGradedView {
	v := &views.ReviewGradedView{SubmitLabel: loc.T("review.graded.submit"), AnswerLabel: loc.T("a11y.review.answer_input")}
	switch kind {
	case "typed":
		v.TypeAttr = "text"
		v.Placeholder = loc.T("review.graded.placeholder")
	case "numeric":
		v.TypeAttr = "text"
		v.InputMode = "decimal"
		v.Placeholder = loc.T("review.graded.placeholder_number")
	case "choice_single":
		v.TypeAttr = "radio"
		v.Options = optionViews(fields)
	case "choice_multi":
		v.TypeAttr = "checkbox"
		v.Options = optionViews(fields)
	case "true_false":
		v.TypeAttr = "radio"
		v.Options = []views.ReviewGradedOption{
			{Value: "true", Label: loc.T("review.graded.true")},
			{Value: "false", Label: loc.T("review.graded.false")},
		}
	default:
		return nil
	}
	return v
}

// optionViews 把 note 的 options[] 转成带 0 基索引的选项控件（choice_* 专用）。
func optionViews(fields map[string]any) []views.ReviewGradedOption {
	opts := optionTexts(fields)
	out := make([]views.ReviewGradedOption, 0, len(opts))
	for i, text := range opts {
		out = append(out, views.ReviewGradedOption{Value: strconv.Itoa(i), Label: text})
	}
	return out
}

// optionTexts 读取 options 字段，兼容 JSON 反序列化的 []any 与 Go 侧构造的 []string。
func optionTexts(fields map[string]any) []string {
	switch v := fields["options"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// buildGradeInput 按题型构造判分器需要的输入结构。判分器各自断言自己的输入类型；
// 只有 web 层知道表单字段如何映射到题型输入，因此映射集中在这里（不改核心管线）。
func buildGradeInput(kind string, fields map[string]any, mapping *cardtype.GradeMapping, form url.Values) (any, error) {
	gc := cardtype.GradeContext{Fields: fields, Mapping: mapping}
	switch kind {
	case "typed":
		return cardtype.TypedInput{GradeContext: gc, Answer: form.Get("answer")}, nil
	case "numeric":
		return cardtype.NumericInput{GradeContext: gc, Answer: form.Get("answer")}, nil
	case "choice_single":
		idx, err := strconv.Atoi(strings.TrimSpace(form.Get("answer")))
		if err != nil {
			return nil, fmt.Errorf("choice_single answer is not an option index")
		}
		return cardtype.ChoiceSingleInput{GradeContext: gc, Selected: idx}, nil
	case "choice_multi":
		selected, err := parseSelectedIndices(form["answer"])
		if err != nil {
			return nil, err
		}
		return cardtype.ChoiceMultiInput{GradeContext: gc, Selected: selected}, nil
	case "true_false":
		answer, err := parseBoolAnswer(form.Get("answer"))
		if err != nil {
			return nil, err
		}
		return cardtype.TrueFalseInput{GradeContext: gc, Answer: &answer}, nil
	default:
		return nil, fmt.Errorf("card type %q has no grading input", kind)
	}
}

// parseSelectedIndices 解析多选的 0 基索引集合；空选择是合法作答（记 0 分）。
func parseSelectedIndices(raw []string) ([]int, error) {
	out := make([]int, 0, len(raw))
	for _, item := range raw {
		idx, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("choice_multi answer is not an option index")
		}
		out = append(out, idx)
	}
	return out, nil
}

// parseBoolAnswer 解析判断题作答；模板提交 "true"/"false"。
func parseBoolAnswer(raw string) (bool, error) {
	b, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("true_false answer is not a boolean")
	}
	return b, nil
}

// reviewGradedAnswer 处理一次作答类提交（M3-12）：构造判分输入、调判分器、按 preset
// 映射得到档位，并在单事务里以 grade_source='typed' 写入 reviews（含 grade_detail_json）。
// 判分后不直接换卡，而是返回结果面板（正确答案 + 判分细节），用户点“继续”才进入下一张。
//
// 输入无法判分（ok=false）或输入不合法时不写库：以局部化错误渲染当前卡，让用户重试。
// reviewRevealedAnswer 处理"已揭示答案后继续"：不判分，按 Again 记一条自评日志。
//
// 为什么要单开一条路径而不是复用 reviewGradedAnswer：后者的评分来自判分器，而这里**必须**
// 记 0 分（Again）——否则用户点开答案再输入就等于白拿 Good/Easy（DESIGN.md §8.2）。
// grade_source 记 self：这次评分确实来自用户（揭示即放弃作答），没有任何机器判分发生。
func (s *Server) reviewRevealedAnswer(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, card *store.Card, note *store.Note) {
	ctx := c.Request.Context()
	done := parseDone(c.PostForm("done"))
	// 判分提交始终用被评卡所属卡组的预设调度；范围只影响队列构建（DESIGN.md §3.3）。
	sched, err := s.schedulerFor(ctx, user.ID, []uint64{note.DeckID})
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, scope, done, "begin review transaction failed", tx.Error)
		return
	}
	if _, err := schedule.Submit(ctx, tx, schedule.SubmitInput{
		CardID:          card.ID,
		UserID:          user.ID,
		Rating:          schedule.Again,
		ExpectedVersion: parseDone(c.PostForm("expected_version")),
		ElapsedMS:       parseElapsed(c.PostForm("elapsed_ms")),
		GradeSource:     schedule.GradeSourceSelf,
		Location:        userLocation(user),
		DayCutoffHour:   user.DayCutoffHour,
		Scheduler:       sched,
		Now:             time.Now().UTC(),
	}); err != nil {
		_ = tx.Rollback().Error
		s.renderReviewError(c, loc, user, scope, done, "submit revealed review failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, scope, done, "commit revealed review failed", err)
		return
	}
	// 直接换下一张：没有判分结果面板可看（答案已经揭示过了）。
	area, err := s.reviewArea(c, loc, user, scope, done+1, "")
	if err != nil {
		s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

func (s *Server) reviewGradedAnswer(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, card *store.Card, note *store.Note) {
	ctx := c.Request.Context()
	done := parseDone(c.PostForm("done"))
	g, graded := graderFor(note.Kind)
	if !graded {
		s.renderReviewError(c, loc, user, scope, done, "graded submit for a card without a grader", nil)
		return
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		s.renderReviewError(c, loc, user, scope, done, "parse note fields for grading failed", err)
		return
	}
	input, err := buildGradeInput(note.Kind, fields, s.gradeMappingFor(ctx, note.DeckID), c.Request.PostForm)
	if err != nil {
		s.renderGradedInputError(c, loc, user, scope, done)
		return
	}
	rating, detail, ok := g.Grade(input)
	if !ok {
		s.renderGradedInputError(c, loc, user, scope, done)
		return
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		s.renderReviewError(c, loc, user, scope, done, "marshal grade detail failed", err)
		return
	}
	raw := string(detailJSON)

	// 判分提交始终用被评卡所属卡组的预设调度；范围只影响队列构建（DESIGN.md §3.3）。
	sched, err := s.schedulerFor(ctx, user.ID, []uint64{note.DeckID})
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, scope, done, "begin review transaction failed", tx.Error)
		return
	}
	if _, err := schedule.Submit(ctx, tx, schedule.SubmitInput{
		CardID:          card.ID,
		UserID:          user.ID,
		Rating:          schedule.Rating(rating),
		ExpectedVersion: parseDone(c.PostForm("expected_version")),
		ElapsedMS:       parseElapsed(c.PostForm("elapsed_ms")),
		GradeSource:     schedule.GradeSourceTyped,
		GradeDetailJSON: &raw,
		Location:        userLocation(user),
		DayCutoffHour:   user.DayCutoffHour,
		Scheduler:       sched,
		Now:             time.Now().UTC(),
	}); err != nil {
		_ = tx.Rollback().Error
		s.renderReviewError(c, loc, user, scope, done, "submit graded review failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, scope, done, "commit graded review failed", err)
		return
	}

	area, err := s.reviewArea(c, loc, user, scope, done+1, "")
	if err != nil {
		s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 结果面板展示的是刚作答的这张卡（队列里已被重排），因此覆盖成当前卡视图。
	cur, err := s.cardView(ctx, user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		s.logger.Error("render graded card failed", "card_id", card.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	area.Card = cur
	area.HasCard = true
	area.Empty = false
	area.Graded = nil
	area.Result = s.gradedResultView(loc, note.Kind, fields, cur, detail, s.gradeMappingFor(ctx, note.DeckID))
	// 结果面板是"已作答、只差继续"的状态：回车/空格/1–4 都走继续，提示串也得跟着换。
	area.ShortcutsHint = loc.T("review.shortcuts_result")
	renderReviewArea(c, area)
}

// renderGradedInputError 以局部化提示重新渲染当前卡，且不写任何进度（输入无法判分）。
func (s *Server) renderGradedInputError(c *gin.Context, loc *i18n.Localizer, user *store.User, scope reviewScope, done int) {
	area, err := s.reviewArea(c, loc, user, scope, done, loc.T("review.graded.error_input"))
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderReviewArea(c, area)
}

// gradedResultView 组装判分结果面板：判定来自分数与映射阈值，细节按题型本地化。
func (s *Server) gradedResultView(loc *i18n.Localizer, kind string, fields map[string]any, card *views.ReviewCardView, detail map[string]any, mapping *cardtype.GradeMapping) *views.ReviewResultView {
	m := cardtype.DefaultGradeMapping()
	if mapping != nil {
		m = *mapping
	}
	score := 0.0
	if v, ok := detail["score"].(float64); ok {
		score = v
	}
	verdict := loc.T("review.graded.incorrect")
	status := "incorrect"
	if score >= m.FullThreshold {
		verdict = loc.T("review.graded.correct")
		status = "correct"
	} else if score > m.NoneThreshold {
		verdict = loc.T("review.graded.partial")
		status = "partial"
	}
	return &views.ReviewResultView{
		VerdictStatus: status,
		VerdictLabel:  verdict,
		ScoreLabel:    loc.T("review.graded.score"),
		Score:         strconv.FormatFloat(score*100, 'f', 0, 64) + "%",
		AnswerLabel:   loc.T("review.graded.correct_answer"),
		AnswerHTML:    card.BackHTML,
		DetailLines:   gradedDetailLines(loc, kind, fields, detail),
		ContinueLabel: loc.T("review.graded.continue"),
	}
}

// gradedDetailLines 把判分细节本地化成标签/值对，展示用户的作答与解析结果。
func gradedDetailLines(loc *i18n.Localizer, kind string, fields map[string]any, detail map[string]any) []views.ReviewDetailLine {
	givenLabel := loc.T("review.graded.given")
	lines := make([]views.ReviewDetailLine, 0, 2)
	switch kind {
	case "typed", "numeric":
		given, _ := detail["given"].(string)
		lines = append(lines, views.ReviewDetailLine{Label: givenLabel, Value: given})
		if parsed, ok := detail["parsed_answer"]; ok {
			lines = append(lines, views.ReviewDetailLine{Label: loc.T("review.graded.parsed"), Value: numberText(parsed)})
		}
	case "choice_single":
		opts := optionTexts(fields)
		value := ""
		if idx, ok := detail["selected"].(int); ok && idx >= 0 && idx < len(opts) {
			value = opts[idx]
		}
		lines = append(lines, views.ReviewDetailLine{Label: givenLabel, Value: value})
	case "choice_multi":
		opts := optionTexts(fields)
		picked := make([]string, 0, len(opts))
		if idxs, ok := detail["selected"].([]int); ok {
			for _, idx := range idxs {
				if idx >= 0 && idx < len(opts) {
					picked = append(picked, opts[idx])
				}
			}
		}
		lines = append(lines, views.ReviewDetailLine{Label: givenLabel, Value: strings.Join(picked, ", ")})
	case "true_false":
		value := loc.T("review.graded.false")
		if b, ok := detail["selected"].(bool); ok && b {
			value = loc.T("review.graded.true")
		}
		lines = append(lines, views.ReviewDetailLine{Label: givenLabel, Value: value})
	}
	return lines
}

// numberText 以最简形式输出数值（解析后的作答），避免 50 显示成 50.000000。
func numberText(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(n), 'g', -1, 64)
	case int:
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}
