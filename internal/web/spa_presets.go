package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 的调度预设接口（DESIGN.md §3.5、§8.1）的 JSON 版本。
//
// 默认预设的补齐、优化门槛、单并发入队、状态与回退全部调用同一批 store/jobs 方法，
// 这里不复制调度、门槛或作业编排逻辑。SSR 预设页删除后，
// 这批 JSON 端点就是预设功能唯一的读写路径。
//
// 所有端点只接受浏览器会话（spaProfileSessionOnly 拒绝 bearer 与 API Key）：预设是个人数据，
// 与卡组设置、统计明细同一安全边界。写操作一律过会话 CSRF。

// spaOptimizeGate 是优化门槛在响应里的形态（DESIGN.md §3.5）。
// shortfall 只在 eligible 为 false 时有意义，等于「还差多少条」。
type spaOptimizeGate struct {
	Reviews   int64 `json:"reviews"`
	Min       int   `json:"min"`
	Shortfall int64 `json:"shortfall"`
	Eligible  bool  `json:"eligible"`
}

// spaFitMetrics 是优化前后各一次拟合的指标（DESIGN.md §3.5）。
type spaFitMetrics struct {
	LogLoss float64 `json:"log_loss"`
	RMSE    float64 `json:"rmse"`
	Items   int     `json:"items"`
}

// spaOptimizeResult 是 optimize 作业 result_json 的 JSON 投影。
// verdict 由服务端按 store.OptimizeResult 的三态判据算出，前端据稳定枚举查语言包，
// 不复制 Improved/SampleSufficient 的规则，避免两边判据漂移（M9-12）。
type spaOptimizeResult struct {
	ReviewsUsed int64         `json:"reviews_used"`
	Weights     []float64     `json:"weights"`
	FitBefore   spaFitMetrics `json:"fit_before"`
	FitAfter    spaFitMetrics `json:"fit_after"`
	OptimizedAt *string       `json:"optimized_at"`
	Verdict     string        `json:"verdict"`
}

// spaJob 是作业状态在响应里的形态；stage/log_tail/error 为空时是 JSON null。
type spaJob struct {
	ID      uint64             `json:"id"`
	Status  string             `json:"status"`
	Stage   *string            `json:"stage"`
	LogTail *string            `json:"log_tail"`
	Error   *string            `json:"error"`
	Result  *spaOptimizeResult `json:"result"`
}

// spaPreset 是单个预设的 JSON 形态，同时携带其权重来源与在途作业。
// job 只在该预设确有在途（queued/running）作业时出现，供刷新页面后续上轮询。
type spaPreset struct {
	ID                  uint64  `json:"id"`
	Name                string  `json:"name"`
	DesiredRetention    float64 `json:"desired_retention"`
	LearningSteps       string  `json:"learning_steps"`
	RelearningSteps     string  `json:"relearning_steps"`
	MaximumIntervalDays int     `json:"maximum_interval_days"`
	EnableFuzz          bool    `json:"enable_fuzz"`
	// WeightsOptimized 与 SSR 的判据一致：weights_json 非空即表示已优化。
	WeightsOptimized   bool    `json:"weights_optimized"`
	WeightsOptimizedAt *string `json:"weights_optimized_at"`
	WeightsReviewCount *int    `json:"weights_review_count"`
	// WeightsRaw 是数据库 weights_json 原文（字符串），与页面显示的值逐字节一致。
	WeightsRaw *string `json:"weights_raw"`
	Job        *spaJob `json:"job"`
}

// spaPresetListResponse 是 GET /api/v1/presets 与创建/编辑/回退共用的响应体。
type spaPresetListResponse struct {
	Presets []spaPreset     `json:"presets"`
	Gate    spaOptimizeGate `json:"gate"`
}

// spaOptimizeResponse 是触发优化与轮询状态共用的响应体：在途作业 + 最新门槛。
type spaOptimizeResponse struct {
	Job  *spaJob         `json:"job"`
	Gate spaOptimizeGate `json:"gate"`
}

// spaPresetRequest 是创建/编辑的请求体。
// EnableFuzz 用 *bool 区分「未提供」与「显式 false」；未提供一律拒绝，避免把缺字段
// 静默当成关闭（AGENTS.md §2.3 第 9 条：带库默认值的布尔列必须显式写）。
type spaPresetRequest struct {
	Name                string  `json:"name"`
	DesiredRetention    float64 `json:"desired_retention"`
	LearningSteps       string  `json:"learning_steps"`
	RelearningSteps     string  `json:"relearning_steps"`
	MaximumIntervalDays int     `json:"maximum_interval_days"`
	EnableFuzz          *bool   `json:"enable_fuzz"`
}

// spaPresetErrorMessages 是预设接口错误 code 的稳定英文兜底文案（AGENTS.md §2.1）。
// 前端按 code 映射本地化提示，不解析这里的 message。
var spaPresetErrorMessages = map[string]string{
	"invalid_request":             "The preset request is invalid.",
	"preset_name_required":        "The preset name is required.",
	"preset_retention_invalid":    "The desired retention must be between 0 and 1 (exclusive).",
	"preset_max_interval_invalid": "The maximum interval must be a positive number of days.",
	"preset_steps_invalid":        "The learning steps are invalid.",
	"preset_save_failed":          "The preset could not be saved.",
	"not_found":                   "The requested preset was not found.",
	"insufficient_reviews":        "There are not enough reviews to optimise yet.",
	"optimize_conflict":           "Another optimisation job is already running.",
	"preset_in_use":               "This preset is used by one or more decks and cannot be deleted.",
	"preset_default_protected":    "The default preset cannot be deleted.",
	"internal_error":              "An internal error occurred.",
}

// registerSPAPresetRoutes 挂载 SPA 的调度预设接口；写操作过会话 CSRF。
// 只注册 /api/v1/presets* 这批 JSON 端点（/presets 只发应用壳，见 spa_presets_cutover.go）。
// 依赖未装配时跳过，保证 M0 阶段与未装作业执行器的测试仍能构造 Server。
func (s *Server) registerSPAPresetRoutes(router *gin.Engine) {
	if s.sessions == nil || s.presets == nil || s.jobRunner == nil {
		return
	}
	router.GET("/api/v1/presets", s.spaPresetList)
	router.POST("/api/v1/presets", s.sessions.CSRFMiddleware(), s.spaPresetCreate)
	router.PATCH("/api/v1/presets/:id", s.sessions.CSRFMiddleware(), s.spaPresetUpdate)
	router.DELETE("/api/v1/presets/:id", s.sessions.CSRFMiddleware(), s.spaPresetDelete)
	router.POST("/api/v1/presets/:id/optimize", s.sessions.CSRFMiddleware(), s.spaPresetOptimize)
	router.GET("/api/v1/presets/:id/optimize/status", s.spaPresetOptimizeStatus)
	router.POST("/api/v1/presets/:id/optimize/revert", s.sessions.CSRFMiddleware(), s.spaPresetOptimizeRevert)
}

// spaPresetList 返回当前用户的全部预设与优化门槛，仅接受浏览器会话。
func (s *Server) spaPresetList(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	resp, err := s.spaPresetListPayload(c, user.ID)
	if err != nil {
		s.logger.Error("list presets for SPA failed", "user_id", user.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// spaPresetCreate 新建预设。校验失败 400、不写库；成功 200 并回整份列表。
func (s *Server) spaPresetCreate(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	req, code := parseSPAPresetRequest(c)
	if code != "" {
		spaPresetError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	p := store.NewPreset(user.ID, req.Name)
	applySPAPresetRequest(&p, req)
	if err := s.presets.Create(ctx, &p); err != nil {
		s.logger.Info("create preset via SPA rejected", "user_id", user.ID, "error", err)
		spaPresetError(c, http.StatusBadRequest, spaPresetStoreCode(err))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetCreate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention, "via": "spa"},
	})
	s.writeSPAPresetList(c, user.ID, "return presets after SPA create failed")
}

// spaPresetUpdate 编辑预设；只有 owner 能改，他人/不存在的 id 一律 404（不泄露存在性）。
func (s *Server) spaPresetUpdate(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.spaOwnedPreset(c, user.ID)
	if !ok {
		return
	}
	req, code := parseSPAPresetRequest(c)
	if code != "" {
		spaPresetError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	applySPAPresetRequest(p, req)
	if err := s.presets.Update(ctx, user.ID, p); err != nil {
		s.logger.Info("update preset via SPA rejected", "user_id", user.ID, "preset_id", p.ID, "error", err)
		spaPresetError(c, http.StatusBadRequest, spaPresetStoreCode(err))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetUpdate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention, "via": "spa"},
	})
	s.writeSPAPresetList(c, user.ID, "return presets after SPA update failed")
}

// spaPresetDelete 删除预设（仅 owner，默认预设与使用中的预设不可删除）。
func (s *Server) spaPresetDelete(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.spaOwnedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.presets.Delete(ctx, user.ID, p.ID); err != nil {
		if errors.Is(err, store.ErrPresetInUse) {
			spaPresetError(c, http.StatusConflict, "preset_in_use")
			return
		}
		if errors.Is(err, store.ErrDefaultPresetCannotDelete) {
			spaPresetError(c, http.StatusBadRequest, "preset_default_protected")
			return
		}
		s.logger.Error("delete preset via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetDelete,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "via": "spa"},
	})
	s.writeSPAPresetList(c, user.ID, "return presets after SPA delete failed")
}

// spaPresetOptimize 触发优化：先判门槛再入队（复用 jobs.Runner.EnqueueOptimize）。
//
//   - 成功：202 + {job, gate}，前端据此轮询；
//   - 门槛不足：400，code=insufficient_reviews，并把结构化 gate（含 shortfall）一并返回，
//     前端渲染「还差 N 条」——不足门槛不是失败，是「还差多少」；
//   - 已有作业：409，code=optimize_conflict（jobs.HTTPStatus 是 409 语义的唯一来源）。
func (s *Server) spaPresetOptimize(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.spaOwnedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	job, err := s.jobRunner.EnqueueOptimize(ctx, user.ID, p.ID)
	if err != nil {
		var threshold *jobs.ThresholdError
		if errors.As(err, &threshold) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "insufficient_reviews", "message": spaPresetErrorMessages["insufficient_reviews"]},
				"gate": spaOptimizeGate{
					Reviews:   threshold.Reviews,
					Min:       threshold.Min,
					Shortfall: threshold.Shortfall,
					Eligible:  false,
				},
			})
			return
		}
		if errors.Is(err, jobs.ErrAlreadyRunning) {
			spaPresetError(c, jobs.HTTPStatus(err), "optimize_conflict")
			return
		}
		s.logger.Error("enqueue optimize via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimize,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"job_id": job.ID, "via": "spa"},
	})
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate after SPA enqueue failed", "user_id", user.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusAccepted, spaOptimizeResponse{Job: s.spaJobPayload(job), Gate: spaGatePayload(gate)})
}

// spaPresetOptimizeStatus 按 ?job= 读取作业状态：只允许看到本预设自己的作业，
// 不匹配的 id 一律按「无作业」返回（job 为 null），不泄露其它预设/作业的存在性。
func (s *Server) spaPresetOptimizeStatus(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.spaOwnedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	job := s.optimizeJobFor(ctx, p.ID, c.Query("job"))
	gate, err := store.GateOptimize(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate for SPA status failed", "user_id", user.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	resp := spaOptimizeResponse{Gate: spaGatePayload(gate)}
	if job != nil {
		resp.Job = s.spaJobPayload(job)
	}
	c.JSON(http.StatusOK, resp)
}

// spaPresetOptimizeRevert 一键回退默认权重：清空三列（store.ResetPresetWeights），
// 留一条审计，然后回整份列表——权重来源、优化时间与条数因此永远来自同一次数据库读取。
func (s *Server) spaPresetOptimizeRevert(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.spaOwnedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.presets.ResetPresetWeights(ctx, user.ID, p.ID); err != nil {
		s.logger.Error("reset preset weights via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimizeRevert,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"via": "spa"},
	})
	s.writeSPAPresetList(c, user.ID, "return presets after SPA revert failed")
}

// spaPresetListPayload 组装列表响应：确保默认预设存在、读取门槛、按在途作业标注卡片。
// 取数与门槛判定收敛在这一个方法：store.EnsureDefaultPreset + store.GateOptimize，
// 同一用户的预设列表与门槛只有这一份来源。
func (s *Server) spaPresetListPayload(c *gin.Context, userID uint64) (spaPresetListResponse, error) {
	ctx := c.Request.Context()
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		return spaPresetListResponse{}, err
	}
	gate, err := store.GateOptimize(ctx, s.db, userID)
	if err != nil {
		return spaPresetListResponse{}, err
	}
	// 单并发：全局至多一个在途作业；它命中本预设时附到卡片上，刷新后可续轮询。
	active, err := s.jobStore.Active(ctx)
	if err != nil {
		return spaPresetListResponse{}, err
	}
	var activeJob *store.Job
	if active != nil && active.Kind == jobs.KindOptimize && active.TargetID != nil {
		activeJob = active
	}
	out := spaPresetListResponse{
		Presets: make([]spaPreset, 0, len(presets)),
		Gate:    spaGatePayload(gate),
	}
	for i := range presets {
		p := &presets[i]
		item := spaPresetPayload(p)
		if activeJob != nil && p.ID == *activeJob.TargetID {
			item.Job = s.spaJobPayload(activeJob)
		}
		out.Presets = append(out.Presets, item)
	}
	return out, nil
}

// writeSPAPresetList 组装并写出列表响应；组装失败统一 500。logMsg 是英文诊断文本。
func (s *Server) writeSPAPresetList(c *gin.Context, userID uint64, logMsg string) {
	resp, err := s.spaPresetListPayload(c, userID)
	if err != nil {
		s.logger.Error(logMsg, "user_id", userID, "error", err)
		spaPresetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// spaOwnedPreset 解析 :id 并确认预设属于当前用户；非 owner/不存在/解析失败一律 404 JSON，
// 与 ownedPreset 的同口径（不通过状态码泄露他人预设的存在性）。
func (s *Server) spaOwnedPreset(c *gin.Context, userID uint64) (*store.Preset, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		spaPresetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	p, err := s.presets.ByID(c.Request.Context(), id)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logger.Error("load preset for SPA failed", "preset_id", id, "error", err)
			spaPresetError(c, http.StatusInternalServerError, "internal_error")
			return nil, false
		}
		spaPresetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	if p.OwnerUserID != userID {
		spaPresetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	return p, true
}

// spaPresetPayload 把一条预设投影成 JSON；权重判据以 weights_json 是否为空为唯一依据。
func spaPresetPayload(p *store.Preset) spaPreset {
	item := spaPreset{
		ID:                  p.ID,
		Name:                p.Name,
		DesiredRetention:    p.DesiredRetention,
		LearningSteps:       p.LearningSteps,
		RelearningSteps:     p.RelearningSteps,
		MaximumIntervalDays: p.MaximumIntervalDays,
		EnableFuzz:          p.FuzzEnabled(),
	}
	if p.WeightsJSON != nil && *p.WeightsJSON != "" {
		item.WeightsOptimized = true
		raw := *p.WeightsJSON
		item.WeightsRaw = &raw
	}
	if p.WeightsOptimizedAt != nil {
		at := p.WeightsOptimizedAt.UTC().Format(time.RFC3339)
		item.WeightsOptimizedAt = &at
	}
	if p.WeightsReviewCount != nil {
		n := *p.WeightsReviewCount
		item.WeightsReviewCount = &n
	}
	return item
}

// spaGatePayload 把门槛投影成 JSON。
func spaGatePayload(g store.OptimizeGate) spaOptimizeGate {
	return spaOptimizeGate{
		Reviews:   g.Reviews,
		Min:       g.MinReviews,
		Shortfall: g.Shortfall,
		Eligible:  g.Eligible,
	}
}

// spaJobPayload 把作业投影成 JSON；只有 succeeded 且 result_json 可解析时才带 result，
// 解析失败按「无结果」处理并记英文日志，绝不编造指标。
func (s *Server) spaJobPayload(job *store.Job) *spaJob {
	item := &spaJob{
		ID:      job.ID,
		Status:  job.Status,
		Stage:   job.Stage,
		LogTail: job.LogTail,
		Error:   job.Error,
	}
	if job.Status != jobs.StatusSucceeded || job.ResultJSON == nil || *job.ResultJSON == "" {
		return item
	}
	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*job.ResultJSON), &result); err != nil {
		s.logger.Error("decode optimize result for SPA failed", "job_id", job.ID, "error", err)
		return item
	}
	item.Result = spaOptimizeResultPayload(&result)
	return item
}

// spaOptimizeResultPayload 投影拟合报告并给出稳定 verdict（三态，M9-12）。
func spaOptimizeResultPayload(r *store.OptimizeResult) *spaOptimizeResult {
	out := &spaOptimizeResult{
		ReviewsUsed: r.ReviewsUsed,
		Weights:     r.Weights,
		FitBefore:   spaFitMetricsPayload(r.FitBefore),
		FitAfter:    spaFitMetricsPayload(r.FitAfter),
		Verdict:     spaOptimizeVerdict(r),
	}
	if !r.OptimizedAt.IsZero() {
		at := r.OptimizedAt.UTC().Format(time.RFC3339)
		out.OptimizedAt = &at
	}
	return out
}

// spaFitMetricsPayload 逐字段投影拟合指标。
func spaFitMetricsPayload(m store.FitMetrics) spaFitMetrics {
	return spaFitMetrics{LogLoss: m.LogLoss, RMSE: m.RMSE, Items: m.Items}
}

// spaOptimizeVerdict 是拟合对比的三态判据（M9-12）：
//   - 两边都算出来且样本足够 -> improved / not_improved；
//   - 两边都算出来但样本太小 -> insufficient_sample；
//   - 没算出来 -> unavailable（前端不渲染结论）。
func spaOptimizeVerdict(r *store.OptimizeResult) string {
	switch {
	case r.FitBefore.SampleSufficient() && r.FitAfter.SampleSufficient():
		if r.Improved() {
			return "improved"
		}
		return "not_improved"
	case r.FitBefore.Available() && r.FitAfter.Available():
		return "insufficient_sample"
	default:
		return "unavailable"
	}
}

// parseSPAPresetRequest 解析 JSON 并做格式校验；返回非空的 code 表示拒绝。
// 校验语义：保留率取开区间 (0,1)、最大间隔为正整数、
// 步骤按语法校验且不接受非正值——store 的校验仍是最后一道防线。
func parseSPAPresetRequest(c *gin.Context) (spaPresetRequest, string) {
	var req spaPresetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return req, "invalid_request"
	}
	req.Name = strings.TrimSpace(req.Name)
	req.LearningSteps = strings.TrimSpace(req.LearningSteps)
	req.RelearningSteps = strings.TrimSpace(req.RelearningSteps)
	switch {
	case req.Name == "":
		return req, "preset_name_required"
	case req.DesiredRetention <= 0 || req.DesiredRetention >= 1:
		return req, "preset_retention_invalid"
	case req.MaximumIntervalDays <= 0:
		return req, "preset_max_interval_invalid"
	case !validStepSpec(req.LearningSteps) || !validStepSpec(req.RelearningSteps):
		return req, "preset_steps_invalid"
	case req.EnableFuzz == nil:
		// 布尔列带库默认值，缺字段一律拒绝，绝不静默当成 false（AGENTS.md §2.3 第 9 条）。
		return req, "invalid_request"
	}
	return req, ""
}

// applySPAPresetRequest 把请求字段写进预设模型；EnableFuzz 显式取地址以保留 false。
func applySPAPresetRequest(p *store.Preset, req spaPresetRequest) {
	p.Name = req.Name
	p.DesiredRetention = req.DesiredRetention
	p.LearningSteps = req.LearningSteps
	p.RelearningSteps = req.RelearningSteps
	p.MaximumIntervalDays = req.MaximumIntervalDays
	fuzz := true
	if req.EnableFuzz != nil {
		fuzz = *req.EnableFuzz
	}
	p.EnableFuzz = &fuzz
}

// spaPresetStoreCode 把 store 的写入校验错误映射成稳定 code（写入前已在 handler 校验，
// 这里只是 store 兜底错误到 JSON code 的翻译）。
func spaPresetStoreCode(err error) string {
	switch {
	case errors.Is(err, store.ErrPresetNameRequired):
		return "preset_name_required"
	case errors.Is(err, store.ErrInvalidDesiredRetention):
		return "preset_retention_invalid"
	case errors.Is(err, store.ErrInvalidMaximumInterval):
		return "preset_max_interval_invalid"
	default:
		return "preset_save_failed"
	}
}

// spaPresetError 写出预设接口的错误包壳；message 为稳定英文，前端按 code 映射本地化提示。
func spaPresetError(c *gin.Context, status int, code string) {
	msg := spaPresetErrorMessages[code]
	if msg == "" {
		msg = code
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}
