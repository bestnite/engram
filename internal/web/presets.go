package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerPresetRoutes 挂载调度预设页与参数优化入口（M9-4，DESIGN.md §3.5、§8.1）。
//
// 优化是单并发的后台作业：入队成功返回 202 与一张带轮询的卡片；阈值不足返回 400 并
// 在卡片里写明「还差 N 条」；已有作业在途返回 409（jobs.HTTPStatus 是 409 语义的唯一来源）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerPresetRoutes(router *gin.Engine) {
	if s.sessions == nil || s.presets == nil || s.jobRunner == nil {
		return
	}
	router.GET("/presets", s.presetList)
	// 写操作一律过 CSRF（DESIGN.md §4.3、§11）。
	// 预设的新建与编辑（M3-14）：创建/编辑只改调度参数，卡组按 id 引用，无需迁移或重排。
	router.POST("/presets", s.sessions.CSRFMiddleware(), s.presetCreate)
	router.POST("/presets/:id", s.sessions.CSRFMiddleware(), s.presetUpdate)
	router.POST("/presets/:id/optimize", s.sessions.CSRFMiddleware(), s.presetOptimize)
	router.GET("/presets/:id/optimize/status", s.presetOptimizeStatus)
	router.POST("/presets/:id/optimize/revert", s.sessions.CSRFMiddleware(), s.presetOptimizeRevert)
}

// presetList 渲染当前用户的预设列表；匿名访问被重定向到登录页。
func (s *Server) presetList(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.renderPresetList(c, loc, user.ID, presetListRender{Status: http.StatusOK})
}

// presetListRender 是一次预设页渲染的差异部分：正常展示、创建校验失败或编辑校验失败。
// 校验失败时把原始输入回填进对应对话框，用户不必重打；状态码由 Status 决定。
type presetListRender struct {
	Status     int
	CreateErr  string
	CreateForm presetForm
	EditErrID  uint64
	EditErr    string
	EditForm   presetForm
}

// renderPresetList 是预设页唯一的渲染入口：确保默认预设存在、读取门槛与当前预设，
// 组装新建对话框与每张卡片的编辑对话框，然后整页输出。
func (s *Server) renderPresetList(c *gin.Context, loc *i18n.Localizer, userID uint64, r presetListRender) {
	ctx := c.Request.Context()
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 门槛按用户（复习日志归属者）判定一次，供所有卡片显示可用条数与门槛。
	gate, err := store.GateOptimize(ctx, s.db, userID)
	if err != nil {
		s.logger.Error("evaluate optimize gate failed", "user_id", userID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	csrf := sessionCSRF(c)
	cards := make([]views.PresetCardData, 0, len(presets))
	for i := range presets {
		card := s.presetCard(ctx, loc, &presets[i], gate, nil, csrf)
		// 编辑校验失败时只让目标卡片的对话框展开并回填原始输入。
		if r.EditErrID != 0 && presets[i].ID == r.EditErrID {
			card.EditError = r.EditErr
			card.EditValues = presetFormValues(r.EditForm)
		}
		cards = append(cards, card)
	}
	createForm := defaultPresetForm()
	if r.CreateErr != "" {
		createForm = r.CreateForm
	}
	data := views.PresetListData{
		Layout:         s.pageLayout(c, loc, "presets.title"),
		Heading:        loc.T("presets.heading"),
		EmptyText:      loc.T("presets.empty"),
		Cards:          cards,
		NewButtonLabel: loc.T("presets.new.button"),
		NewHeading:     loc.T("presets.new.heading"),
		CreateAction:   "/presets",
		Labels:         presetFormLabels(loc),
		NewValues:      presetFormValues(createForm),
		ErrorMessage:   r.CreateErr,
		CSRF:           csrf,
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(r.Status)
	if err := views.PresetListPage(data).Render(ctx, c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// presetCreate 处理「新建预设」：校验表单、落库并回到列表页。
// 校验失败时整页重渲染（400），把错误与原始输入放进新建对话框，不写库。
func (s *Server) presetCreate(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	form, errKey := parsePresetForm(c)
	if errKey != "" {
		s.renderPresetList(c, loc, user.ID, presetListRender{
			Status: http.StatusBadRequest, CreateErr: loc.T(errKey), CreateForm: form,
		})
		return
	}
	p := store.NewPreset(user.ID, form.Name)
	applyPresetForm(&p, form)
	if err := s.presets.Create(ctx, &p); err != nil {
		s.logger.Info("create preset rejected", "user_id", user.ID, "error", err)
		s.renderPresetList(c, loc, user.ID, presetListRender{
			Status: http.StatusBadRequest, CreateErr: loc.T(presetStoreErrorKey(err)), CreateForm: form,
		})
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetCreate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention},
	})
	c.Redirect(http.StatusSeeOther, "/presets")
}

// presetUpdate 处理「编辑预设」：只有 owner 能改，他人/不存在的 id 一律 404（不泄露存在性）。
// 编辑只改调度参数：卡组按 id 引用该预设，因此不需要数据迁移，也不重排任何到期日。
func (s *Server) presetUpdate(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	form, errKey := parsePresetForm(c)
	if errKey != "" {
		s.renderPresetList(c, loc, user.ID, presetListRender{
			Status: http.StatusBadRequest, EditErrID: p.ID, EditErr: loc.T(errKey), EditForm: form,
		})
		return
	}
	applyPresetForm(p, form)
	if err := s.presets.Update(ctx, user.ID, p); err != nil {
		s.logger.Info("update preset rejected", "user_id", user.ID, "preset_id", p.ID, "error", err)
		s.renderPresetList(c, loc, user.ID, presetListRender{
			Status: http.StatusBadRequest, EditErrID: p.ID, EditErr: loc.T(presetStoreErrorKey(err)), EditForm: form,
		})
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetUpdate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention},
	})
	c.Redirect(http.StatusSeeOther, "/presets")
}

// presetForm 是创建/编辑表单解析后的字段集合。
type presetForm struct {
	Name                string
	DesiredRetention    float64
	LearningSteps       string
	RelearningSteps     string
	MaximumIntervalDays int
	Fuzz                bool
}

// defaultPresetForm 返回新建对话框的预填值：与 store 的文档化默认值一致。
func defaultPresetForm() presetForm {
	return presetForm{
		DesiredRetention:    store.DefaultDesiredRetention,
		LearningSteps:       store.DefaultLearningSteps,
		RelearningSteps:     store.DefaultRelearningSteps,
		MaximumIntervalDays: store.DefaultMaximumIntervalDays,
		Fuzz:                store.DefaultEnableFuzz,
	}
}

// parsePresetForm 解析并做格式校验；返回非空的本地化错误 key 表示拒绝。
// 语义校验与 store 的 validatePresetForWrite 对齐，但目标保留率按验收要求收紧为 (0,1) 开区间
// （store 允许到 1）：越界在写库前就被拦下，store 的校验仍是最后一道防线。
func parsePresetForm(c *gin.Context) (presetForm, string) {
	f := presetForm{
		Name:            strings.TrimSpace(c.PostForm("name")),
		LearningSteps:   strings.TrimSpace(c.PostForm("learning_steps")),
		RelearningSteps: strings.TrimSpace(c.PostForm("relearning_steps")),
		Fuzz:            c.PostForm("enable_fuzz") != "",
	}
	if f.Name == "" {
		return f, "presets.form.error.name_required"
	}
	retention, err := strconv.ParseFloat(strings.TrimSpace(c.PostForm("desired_retention")), 64)
	if err != nil || retention <= 0 || retention >= 1 {
		return f, "presets.form.error.retention"
	}
	f.DesiredRetention = retention
	days, err := strconv.Atoi(strings.TrimSpace(c.PostForm("maximum_interval_days")))
	if err != nil || days <= 0 {
		return f, "presets.form.error.max_interval"
	}
	f.MaximumIntervalDays = days
	if !validStepSpec(f.LearningSteps) || !validStepSpec(f.RelearningSteps) {
		return f, "presets.form.error.steps"
	}
	return f, ""
}

// applyPresetForm 把解析后的字段写进预设模型；EnableFuzz 是 *bool，显式取地址以保留 false。
func applyPresetForm(p *store.Preset, f presetForm) {
	p.Name = f.Name
	p.DesiredRetention = f.DesiredRetention
	p.LearningSteps = f.LearningSteps
	p.RelearningSteps = f.RelearningSteps
	p.MaximumIntervalDays = f.MaximumIntervalDays
	fuzz := f.Fuzz
	p.EnableFuzz = &fuzz
}

// validStepSpec 判断学习/再学习步骤串是否可解析；空串合法（关闭步骤）。
// 文法与 internal/schedule 的 parseSteps 一致（逗号分隔、s/m/h/d 后缀、缺省分钟），
// 但额外要求每个步骤为正：调度器会把 "-5m" 这类负数当合法输入，验收要求在此拒绝。
// 这里只判断「可解析」，不复制调度逻辑——调度器读库时仍走自己的 parseSteps。
func validStepSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := len(part)
		for idx > 0 {
			ch := part[idx-1]
			if (ch >= '0' && ch <= '9') || ch == '.' {
				break
			}
			idx--
		}
		num, unit := part[:idx], strings.ToLower(part[idx:])
		value, err := strconv.ParseFloat(num, 64)
		if err != nil || value <= 0 {
			return false
		}
		switch unit {
		case "", "m", "min", "s", "sec", "h", "hr", "d":
		default:
			return false
		}
	}
	return true
}

// presetStoreErrorKey 把 store 的校验错误映射成语言包 key（AGENTS.md §2.1：用户可见文案走语言包）。
func presetStoreErrorKey(err error) string {
	switch {
	case errors.Is(err, store.ErrPresetNameRequired):
		return "presets.form.error.name_required"
	case errors.Is(err, store.ErrInvalidDesiredRetention):
		return "presets.form.error.retention"
	case errors.Is(err, store.ErrInvalidMaximumInterval):
		return "presets.form.error.max_interval"
	default:
		return "presets.form.error.save_failed"
	}
}

// presetFormLabels 一次性取出新建/编辑对话框的全部标签。
func presetFormLabels(loc *i18n.Localizer) views.PresetFormLabels {
	return views.PresetFormLabels{
		Name:            loc.T("presets.form.name"),
		Retention:       loc.T("presets.form.retention"),
		LearningSteps:   loc.T("presets.form.learning_steps"),
		RelearningSteps: loc.T("presets.form.relearning_steps"),
		MaxInterval:     loc.T("presets.form.max_interval"),
		Fuzz:            loc.T("presets.form.fuzz"),
		Save:            loc.T("presets.form.save"),
		Cancel:          loc.T("presets.form.cancel"),
		Close:           loc.T("presets.form.close"),
		EditNote:        loc.T("presets.form.edit_note"),
	}
}

// presetFormValues 把解析后的字段渲染成表单回显值。
func presetFormValues(f presetForm) views.PresetFormValues {
	return views.PresetFormValues{
		Name:            f.Name,
		Retention:       strconv.FormatFloat(f.DesiredRetention, 'f', 2, 64),
		LearningSteps:   f.LearningSteps,
		RelearningSteps: f.RelearningSteps,
		MaxInterval:     strconv.Itoa(f.MaximumIntervalDays),
		Fuzz:            f.Fuzz,
	}
}

// presetValuesFrom 从库里的预设构造编辑表单回显值。
func presetValuesFrom(p *store.Preset) views.PresetFormValues {
	return views.PresetFormValues{
		Name:            p.Name,
		Retention:       strconv.FormatFloat(p.DesiredRetention, 'f', 2, 64),
		LearningSteps:   p.LearningSteps,
		RelearningSteps: p.RelearningSteps,
		MaxInterval:     strconv.Itoa(p.MaximumIntervalDays),
		Fuzz:            p.FuzzEnabled(),
	}
}

// presetOptimize 处理「开始优化」：先判门槛再入队。
//
// 三种结果都以卡片片段回应（htmx 就地替换，无需整页刷新）：
//   - 成功：202 + 卡片（带轮询）；
//   - 阈值不足：400 + 卡片（写明还差多少条，来自 jobs.ThresholdError.Shortfall）；
//   - 已有作业：409 + 卡片（单并发，jobs.ErrAlreadyRunning）。
func (s *Server) presetOptimize(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	csrf := sessionCSRF(c)

	job, err := s.jobRunner.EnqueueOptimize(ctx, user.ID, p.ID)
	if err != nil {
		var threshold *jobs.ThresholdError
		if errors.As(err, &threshold) {
			// 不足门槛不是「失败」，而是「还差 N 条」：把差额展示出来，用户才知道要攒多少。
			card := s.presetCard(ctx, loc, p, gate, nil, csrf)
			card.Shortfall = loc.Tf("presets.optimize.shortfall", map[string]any{
				"count": threshold.Shortfall,
				"have":  threshold.Reviews,
				"min":   threshold.Min,
			})
			s.renderPresetCard(c, card, http.StatusBadRequest)
			return
		}
		if errors.Is(err, jobs.ErrAlreadyRunning) {
			card := s.presetCard(ctx, loc, p, gate, nil, csrf)
			card.Conflict = loc.T("presets.optimize.conflict")
			s.renderPresetCard(c, card, jobs.HTTPStatus(err))
			return
		}
		s.logger.Error("enqueue optimize failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimize,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"job_id": job.ID},
	})
	s.renderPresetCard(c, s.presetCard(ctx, loc, p, gate, job, csrf), http.StatusAccepted)
}

// presetOptimizeStatus 是 htmx 轮询端点：按 ?job= 读取作业状态并重渲染整张卡片。
//
// 整卡重渲染（而不是单独的状态块）让权重来源、结果摘要与状态永远来自同一次数据库读取，
// 避免「状态说完成、权重还显示默认」这类自相矛盾的画面（M9-4 验收点）。
func (s *Server) presetOptimizeStatus(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	job := s.optimizeJobFor(ctx, p.ID, c.Query("job"))
	s.renderPresetCard(c, s.presetCard(ctx, loc, p, gate, job, sessionCSRF(c)), http.StatusOK)
}

// presetOptimizeRevert 一键回退默认权重：清空三列后重渲染卡片（M9-4、DESIGN.md §3.5）。
func (s *Server) presetOptimizeRevert(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	if err := s.presets.ResetPresetWeights(ctx, user.ID, p.ID); err != nil {
		s.logger.Error("reset preset weights failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimizeRevert,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
	})
	// 重读预设，确保卡片显示的是库里的真实状态（回退后 weights_json 为 NULL）。
	reloaded, err := s.presets.ByID(ctx, p.ID)
	if err != nil {
		s.logger.Error("reload preset after revert failed", "preset_id", p.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.renderPresetCard(c, s.presetCard(ctx, loc, reloaded, gate, nil, sessionCSRF(c)), http.StatusOK)
}

// ownedPreset 解析 :id 并确认预设属于当前用户；否则按「不存在」处理，不泄露他人预设的存在性。
func (s *Server) ownedPreset(c *gin.Context, userID uint64) (*store.Preset, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	p, err := s.presets.ByID(c.Request.Context(), id)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logger.Error("load preset failed", "preset_id", id, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return nil, false
		}
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	if p.OwnerUserID != userID {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, false
	}
	return p, true
}

// optimizeJobFor 按 id 读取优化作业，并校验它确实属于该预设（kind 与 target 都对）。
// 任何不匹配都返回 nil：状态端点只允许看到本预设自己的作业。
func (s *Server) optimizeJobFor(ctx context.Context, presetID uint64, raw string) *store.Job {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return nil
	}
	job, err := s.jobStore.ByID(ctx, id)
	if err != nil {
		return nil
	}
	if job.Kind != jobs.KindOptimize || job.TargetID == nil || *job.TargetID != presetID {
		return nil
	}
	return job
}

// presetCard 组装一张预设卡片的渲染数据。每次调用都从传入的预设快照取值（调用方保证
// 快照是最新读取的），页面因此不会显示与数据库不一致的权重。
func (s *Server) presetCard(ctx context.Context, loc *i18n.Localizer, p *store.Preset, gate store.OptimizeGate, job *store.Job, csrf string) views.PresetCardData {
	_ = ctx
	card := views.PresetCardData{
		ID:               strconv.FormatUint(p.ID, 10),
		Name:             p.Name,
		RetentionLabel:   loc.T("presets.retention_label"),
		RetentionValue:   strconv.FormatFloat(p.DesiredRetention, 'f', 2, 64),
		MaxIntervalLabel: loc.T("presets.max_interval_label"),
		MaxIntervalValue: strconv.Itoa(p.MaximumIntervalDays),
		FuzzLabel:        loc.T("presets.fuzz_label"),
		FuzzValue:        loc.T("presets.fuzz_off"),
		WeightsHeading:   loc.T("presets.weights.heading"),
		WeightsSource:    loc.T("presets.weights.source_default"),
		OptimizeLabel:    loc.T("presets.optimize.button"),
		OptimizeAction:   fmt.Sprintf("/presets/%d/optimize", p.ID),
		StatusURL:        fmt.Sprintf("/presets/%d/optimize/status", p.ID),
		PollTrigger:      "none",
		RevertLabel:      loc.T("presets.revert.button"),
		RevertAction:     fmt.Sprintf("/presets/%d/optimize/revert", p.ID),
		RevertNote:       loc.T("presets.revert.note"),
		RescheduleNote:   loc.T("presets.reschedule.note"),
		ReviewsAvailable: loc.Tf("presets.reviews.available", map[string]any{"count": gate.Reviews}),
		Threshold:        loc.Tf("presets.reviews.threshold", map[string]any{"min": gate.MinReviews}),
		LogTailLabel:     loc.T("presets.optimize.log_tail"),
		// 编辑入口与对话框（M3-14）。
		EditLabel:   loc.T("presets.edit.button"),
		EditHeading: loc.T("presets.edit.heading"),
		EditAction:  fmt.Sprintf("/presets/%d", p.ID),
		Labels:      presetFormLabels(loc),
		EditValues:  presetValuesFrom(p),
		CSRF:        csrf,
	}
	if p.FuzzEnabled() {
		card.FuzzValue = loc.T("presets.fuzz_on")
	}
	// 权重来源：weights_json 是否为空是唯一的判据，与调度器（schedule.NewScheduler）一致。
	if p.WeightsJSON != nil && *p.WeightsJSON != "" {
		card.WeightsSource = loc.T("presets.weights.source_optimized")
		card.WeightsRawLabel = loc.T("presets.weights.raw_label")
		card.WeightsRaw = *p.WeightsJSON
	}
	if p.WeightsOptimizedAt != nil {
		card.WeightsOptimizedAt = loc.Tf("presets.weights.optimized_at", map[string]any{
			"time": p.WeightsOptimizedAt.UTC().Format(time.RFC3339),
		})
	}
	if p.WeightsReviewCount != nil {
		card.WeightsReviewCount = loc.Tf("presets.weights.review_count", map[string]any{
			"count": *p.WeightsReviewCount,
		})
	}
	if job != nil {
		s.applyJobToCard(loc, &card, job)
	}
	return card
}

// applyJobToCard 把作业状态、阶段、日志尾巴与结果摘要叠加到卡片上。
// 只做阶段级状态，不编造百分比（DESIGN.md §3.5）。
func (s *Server) applyJobToCard(loc *i18n.Localizer, card *views.PresetCardData, job *store.Job) {
	switch job.Status {
	case jobs.StatusQueued:
		card.StatusText = loc.T("presets.optimize.queued")
		card.PollTrigger = "every 2s"
	case jobs.StatusRunning:
		card.StatusText = loc.T("presets.optimize.running")
		card.PollTrigger = "every 2s"
		if job.Stage != nil {
			card.StageText = loc.T(stageKey(*job.Stage))
		}
	case jobs.StatusSucceeded:
		card.StatusText = loc.T("presets.optimize.succeeded")
		s.applyResultToCard(loc, card, job)
	case jobs.StatusFailed:
		card.StatusText = loc.T("presets.optimize.failed")
		if job.Error != nil {
			// 失败原因由适配器写出（英文诊断文本），如实展示而不是改写。
			card.ErrorText = *job.Error
		}
	}
	if job.LogTail != nil {
		card.LogTail = *job.LogTail
	}
}

// applyResultToCard 解析 job.result_json 并填入拟合对比（M9-5 的指标存在 job 行上）。
func (s *Server) applyResultToCard(loc *i18n.Localizer, card *views.PresetCardData, job *store.Job) {
	if job.ResultJSON == nil || *job.ResultJSON == "" {
		return
	}
	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*job.ResultJSON), &result); err != nil {
		s.logger.Error("decode optimize result failed", "job_id", job.ID, "error", err)
		return
	}
	card.ResultTitle = loc.T("presets.optimize.result_title")
	card.ResultReviews = loc.Tf("presets.optimize.result_reviews", map[string]any{"count": result.ReviewsUsed})
	// 三态（ROADMAP.md M9-12）：
	//   1. 两边都算出来且样本足够 -> 渲染两个数与「改善/未改善」结论；
	//   2. 两边都算出来但样本太小（< MinFitItems 个 item）-> 只渲染「样本不足，无法判定」，
	//      不渲染两个数、不给结论——一次本就没机会的优化不该被误报成「未改善」；
	//   3. 没算出来（零值）-> 什么都不渲染，否则 Improved() 会把零值判成「未改善」。
	switch {
	case result.FitBefore.SampleSufficient() && result.FitAfter.SampleSufficient():
		card.ResultBefore = fitText(loc, "presets.optimize.result_before", result.FitBefore)
		card.ResultAfter = fitText(loc, "presets.optimize.result_after", result.FitAfter)
		if result.Improved() {
			card.ResultVerdict = loc.T("presets.optimize.improved")
		} else {
			card.ResultVerdict = loc.T("presets.optimize.not_improved")
		}
	case result.FitBefore.Available() && result.FitAfter.Available():
		card.ResultSampleInsufficient = loc.T("presets.optimize.sample_insufficient")
	}
}

// fitText 把拟合指标格式化成一条本地化文本；两个指标都保留四位小数，便于直接比较。
func fitText(loc *i18n.Localizer, key string, m store.FitMetrics) string {
	return loc.Tf(key, map[string]any{
		"logloss": strconv.FormatFloat(m.LogLoss, 'f', 4, 64),
		"rmse":    strconv.FormatFloat(m.RMSE, 'f', 4, 64),
	})
}

// stageKey 把作业阶段名映射到语言包 key；未知阶段显示「未知」而不是裸标识符。
func stageKey(stage string) string {
	switch stage {
	case jobs.StageReadLogs:
		return "presets.optimize.stage.read_logs"
	case jobs.StageTraining:
		return "presets.optimize.stage.training"
	case jobs.StageWriting:
		return "presets.optimize.stage.writing"
	default:
		return "presets.optimize.stage.unknown"
	}
}

// renderPresetCard 写出卡片片段；status 用于把阈值不足/并发冲突渲染成 4xx。
func (s *Server) renderPresetCard(c *gin.Context, data views.PresetCardData, status int) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if err := views.PresetCard(data).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// sessionCSRF 取当前会话绑定的 CSRF token；卡片内的表单需要它。
func sessionCSRF(c *gin.Context) string {
	if sess, ok := auth.CurrentSession(c); ok {
		return sess.CSRFToken
	}
	return ""
}
