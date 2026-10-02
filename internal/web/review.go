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
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
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
	// 作答类题型（M3-12）：题型实现 Grader 时走机器判分，不再依赖四档自评。
	if _, graded := graderFor(note.Kind); graded {
		s.reviewGradedAnswer(c, loc, user, card, note, deck)
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
		Location:        userLocation(user),
		DayCutoffHour:   user.DayCutoffHour,
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
	// “继续”只是换下一张卡：判分已经在提交那一步写入，无需再开事务（M3-12）。
	if action == "next" {
		area, err := s.reviewArea(c, loc, user, deck.ID, done, "")
		if err != nil {
			s.logger.Error("build next card failed", "user_id", user.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		renderReviewArea(c, area)
		return
	}
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
	// 作答类题型渲染输入控件代替四档自评（M3-12）。
	data.Graded = s.gradedViewForCard(c.Request.Context(), loc, items[0])
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
func (s *Server) reviewGradedAnswer(c *gin.Context, loc *i18n.Localizer, user *store.User, card *store.Card, note *store.Note, deck *store.Deck) {
	ctx := c.Request.Context()
	done := parseDone(c.PostForm("done"))
	g, graded := graderFor(note.Kind)
	if !graded {
		s.renderReviewError(c, loc, user, deck.ID, done, "graded submit for a card without a grader", nil)
		return
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "parse note fields for grading failed", err)
		return
	}
	input, err := buildGradeInput(note.Kind, fields, s.gradeMappingFor(ctx, note.DeckID), c.Request.PostForm)
	if err != nil {
		s.renderGradedInputError(c, loc, user, deck.ID, done)
		return
	}
	rating, detail, ok := g.Grade(input)
	if !ok {
		s.renderGradedInputError(c, loc, user, deck.ID, done)
		return
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "marshal grade detail failed", err)
		return
	}
	raw := string(detailJSON)

	sched, err := s.schedulerFor(ctx, user.ID, note.DeckID)
	if err != nil {
		s.logger.Error("load scheduler failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "begin review transaction failed", tx.Error)
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
		s.renderReviewError(c, loc, user, deck.ID, done, "submit graded review failed", err)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.renderReviewError(c, loc, user, deck.ID, done, "commit graded review failed", err)
		return
	}

	area, err := s.reviewArea(c, loc, user, deck.ID, done+1, "")
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
	renderReviewArea(c, area)
}

// renderGradedInputError 以局部化提示重新渲染当前卡，且不写任何进度（输入无法判分）。
func (s *Server) renderGradedInputError(c *gin.Context, loc *i18n.Localizer, user *store.User, deckID uint64, done int) {
	area, err := s.reviewArea(c, loc, user, deckID, done, loc.T("review.graded.error_input"))
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
	if score >= m.FullThreshold {
		verdict = loc.T("review.graded.correct")
	} else if score > m.NoneThreshold {
		verdict = loc.T("review.graded.partial")
	}
	return &views.ReviewResultView{
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
