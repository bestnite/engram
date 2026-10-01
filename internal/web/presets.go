package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/jobs"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
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
	ctx := c.Request.Context()
	presets, err := s.presets.ListByOwner(ctx, user.ID)
	if err != nil {
		s.logger.Error("list presets failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 门槛按用户（复习日志归属者）判定一次，供所有卡片显示可用条数与门槛。
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	cards := make([]views.PresetCardData, 0, len(presets))
	for i := range presets {
		cards = append(cards, s.presetCard(ctx, loc, &presets[i], gate, nil, csrf))
	}
	data := views.PresetListData{
		Layout:    s.pageLayout(c, loc, "presets.title"),
		Heading:   loc.T("presets.heading"),
		Intro:     loc.T("presets.intro"),
		EmptyText: loc.T("presets.empty"),
		Cards:     cards,
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := views.PresetListPage(data).Render(ctx, c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
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
		CSRF:             csrf,
	}
	if p.EnableFuzz {
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
	card.ResultBefore = fitText(loc, "presets.optimize.result_before", result.FitBefore)
	card.ResultAfter = fitText(loc, "presets.optimize.result_after", result.FitAfter)
	if result.Improved() {
		card.ResultVerdict = loc.T("presets.optimize.improved")
	} else {
		card.ResultVerdict = loc.T("presets.optimize.not_improved")
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
