package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the JSON version of the SPA scheduling preset API.
//
// Default-preset backfill, the optimization gate, single-concurrency enqueueing, status and rollback all call the same set of store/jobs methods,
// so scheduling, gate or job-orchestration logic is not duplicated here. With the SSR preset page deleted,
// these JSON endpoints are the only read/write path for the preset feature.
//
// Every endpoint accepts browser sessions only (profileSessionOnly rejects bearer and API keys): presets are personal data,
// on the same security boundary as deck settings and stats detail. Writes always go through session CSRF.

// optimizeGate is the shape of the optimization gate in responses.
// shortfall is only meaningful when eligible is false, and equals "how many more are still needed".
type optimizeGate struct {
	Reviews   int64 `json:"reviews"`
	Min       int   `json:"min"`
	Shortfall int64 `json:"shortfall"`
	Eligible  bool  `json:"eligible"`
}

// fitMetrics holds the metrics of one fit each, before and after optimization.
type fitMetrics struct {
	LogLoss float64 `json:"log_loss"`
	RMSE    float64 `json:"rmse"`
	Items   int     `json:"items"`
}

// optimizeResult is the JSON projection of an optimize job's result_json.
// verdict is computed server-side from store.OptimizeResult's three-state criteria; the frontend looks up the language pack by the stable enum,
// so the Improved/SampleSufficient rules are not duplicated and the criteria cannot drift between the two sides.
type optimizeResult struct {
	ReviewsUsed int64      `json:"reviews_used"`
	Weights     []float64  `json:"weights"`
	FitBefore   fitMetrics `json:"fit_before"`
	FitAfter    fitMetrics `json:"fit_after"`
	OptimizedAt *string    `json:"optimized_at"`
	Verdict     string     `json:"verdict"`
}

// jobView is the shape of a job's status in responses; stage/log_tail/error are JSON null when empty.
type jobView struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Stage   *string         `json:"stage"`
	LogTail *string         `json:"log_tail"`
	Error   *string         `json:"error"`
	Result  *optimizeResult `json:"result"`
}

// presetView is the JSON shape of one preset, carrying both its weight provenance and any in-flight job.
// job appears only when that preset really has an in-flight (queued/running) job, so polling can resume after a page refresh.
// IsDefault keeps the interface from needing to know the storage name of the "default preset" (store.DefaultPresetName is a machine identifier,
// not human-facing copy): both the display name and the "not deletable / not renamable" criteria come from it.
type presetView struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	IsDefault           bool    `json:"is_default"`
	DesiredRetention    float64 `json:"desired_retention"`
	LearningSteps       string  `json:"learning_steps"`
	RelearningSteps     string  `json:"relearning_steps"`
	MaximumIntervalDays int     `json:"maximum_interval_days"`
	EnableFuzz          bool    `json:"enable_fuzz"`
	// WeightsOptimized uses the same criterion as SSR: a non-empty weights_json means optimized.
	WeightsOptimized   bool    `json:"weights_optimized"`
	WeightsOptimizedAt *string `json:"weights_optimized_at"`
	WeightsReviewCount *int    `json:"weights_review_count"`
	// WeightsRaw is the raw database weights_json (a string), byte-identical to what the page displays.
	WeightsRaw *string  `json:"weights_raw"`
	Job        *jobView `json:"job"`
	// Gate is this preset's optimization gate: usable reviews only count records on decks carrying this preset, so each preset has its own.
	Gate optimizeGate `json:"gate"`
}

// presetListResponse is the response body shared by GET /api/v1/presets and create/edit/rollback.
type presetListResponse struct {
	Presets []presetView `json:"presets"`
}

// optimizeResponse is the response body shared by triggering optimization and polling status: the in-flight job plus the latest gate.
type optimizeResponse struct {
	Job  *jobView     `json:"job"`
	Gate optimizeGate `json:"gate"`
}

// presetRequest is the create/edit request body.
// EnableFuzz uses *bool to tell "not provided" from "explicit false"; not provided is always refused, so a missing field is never
// silently taken as off (AGENTS.md §2.3 item 9: boolean columns with database defaults must be written explicitly).
type presetRequest struct {
	Name                string  `json:"name"`
	DesiredRetention    float64 `json:"desired_retention"`
	LearningSteps       string  `json:"learning_steps"`
	RelearningSteps     string  `json:"relearning_steps"`
	MaximumIntervalDays int     `json:"maximum_interval_days"`
	EnableFuzz          *bool   `json:"enable_fuzz"`
}

// presetErrorMessages holds stable English fallback copy for the preset API error codes (AGENTS.md §2.1).
// The frontend maps code to a localized hint and does not parse the message here.
var presetErrorMessages = map[string]string{
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
	"preset_default_protected":    "The default preset cannot be renamed or deleted.",
	"internal_error":              "An internal error occurred.",
}

// registerPresetAPIRoutes mounts the SPA scheduling preset API; writes go through session CSRF.
// Only the /api/v1/presets* JSON endpoints are registered; /presets itself only sends the app shell, rendered by the client router.
// Skipped when the dependency is not wired up, so the M0 stage and tests without a job runner can still construct a Server.
func (s *Server) registerPresetAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.presets == nil || s.jobRunner == nil {
		return
	}
	router.GET("/api/v1/presets", s.presetList)
	router.POST("/api/v1/presets", s.sessions.CSRFMiddleware(), s.presetCreate)
	router.PATCH("/api/v1/presets/:id", s.sessions.CSRFMiddleware(), s.presetUpdate)
	router.DELETE("/api/v1/presets/:id", s.sessions.CSRFMiddleware(), s.presetDelete)
	router.POST("/api/v1/presets/:id/optimize", s.sessions.CSRFMiddleware(), s.presetOptimize)
	router.GET("/api/v1/presets/:id/optimize/status", s.presetOptimizeStatus)
	router.POST("/api/v1/presets/:id/optimize/revert", s.sessions.CSRFMiddleware(), s.presetOptimizeRevert)
}

// presetList returns all of the current user's presets and the optimization gate; accepts browser sessions only.
func (s *Server) presetList(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	resp, err := s.presetListPayload(c, user.ID)
	if err != nil {
		s.logger.Error("list presets for SPA failed", "user_id", user.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// presetCreate creates a preset. Validation failure is 400 with nothing written; success is 200 with the whole list back.
func (s *Server) presetCreate(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	req, code := parsePresetRequest(c)
	if code != "" {
		presetError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	p := store.NewPreset(user.ID, req.Name)
	applyPresetRequest(&p, req)
	if err := s.presets.Create(ctx, &p); err != nil {
		s.logger.Info("create preset via SPA rejected", "user_id", user.ID, "error", err)
		presetError(c, http.StatusBadRequest, presetStoreCode(err))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetCreate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention, "via": "spa"},
	})
	s.writePresetList(c, user.ID, "return presets after SPA create failed")
}

// presetUpdate edits a preset; only the owner may change it, and someone else's / a nonexistent id is always 404 (leaking no existence).
func (s *Server) presetUpdate(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	req, code := parsePresetRequest(c)
	if code != "" {
		presetError(c, http.StatusBadRequest, code)
		return
	}
	ctx := c.Request.Context()
	applyPresetRequest(p, req)
	if err := s.presets.Update(ctx, user.ID, p); err != nil {
		s.logger.Info("update preset via SPA rejected", "user_id", user.ID, "preset_id", p.ID, "error", err)
		presetError(c, http.StatusBadRequest, presetStoreCode(err))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetUpdate,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "desired_retention": p.DesiredRetention, "via": "spa"},
	})
	s.writePresetList(c, user.ID, "return presets after SPA update failed")
}

// presetDelete deletes a preset (owner only; the default preset and a preset in use cannot be deleted).
func (s *Server) presetDelete(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.presets.Delete(ctx, user.ID, p.ID); err != nil {
		if errors.Is(err, store.ErrPresetInUse) {
			presetError(c, http.StatusConflict, "preset_in_use")
			return
		}
		if errors.Is(err, store.ErrDefaultPresetCannotDelete) {
			presetError(c, http.StatusBadRequest, "preset_default_protected")
			return
		}
		s.logger.Error("delete preset via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetDelete,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"name": p.Name, "via": "spa"},
	})
	s.writePresetList(c, user.ID, "return presets after SPA delete failed")
}

// presetOptimize triggers optimization: check the gate first, then enqueue (reusing jobs.Runner.EnqueueOptimize).
//
//   - success: 202 + {job, gate}, which the frontend polls on;
//   - gate short: 400 with code=insufficient_reviews, also returning the structured gate (including shortfall),
//     so the frontend can render "N more to go" — falling short is not a failure, it is "how much is still missing";
//   - a job already exists: 409 with code=optimize_conflict (jobs.HTTPStatus is the single source of the 409 semantics).
func (s *Server) presetOptimize(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	job, err := s.jobRunner.EnqueueOptimize(ctx, user.ID, p.ID)
	if err != nil {
		var threshold *jobs.ThresholdError
		if errors.As(err, &threshold) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": gin.H{"code": "insufficient_reviews", "message": presetErrorMessages["insufficient_reviews"]},
				"gate": optimizeGate{
					Reviews:   threshold.Reviews,
					Min:       threshold.Min,
					Shortfall: threshold.Shortfall,
					Eligible:  false,
				},
			})
			return
		}
		if errors.Is(err, jobs.ErrAlreadyRunning) {
			presetError(c, jobs.HTTPStatus(err), "optimize_conflict")
			return
		}
		s.logger.Error("enqueue optimize via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimize,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"job_id": job.PublicID, "via": "spa"},
	})
	gate, err := store.GateOptimize(ctx, s.db, user.ID, p.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate after SPA enqueue failed", "user_id", user.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusAccepted, optimizeResponse{Job: s.jobPayload(job), Gate: gatePayload(gate)})
}

// presetOptimizeStatus reads a job's status by ?job=: it may only see this preset's own jobs, and
// a non-matching id is always answered as "no job" (job null), leaking no existence of other presets/jobs.
func (s *Server) presetOptimizeStatus(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	job := s.optimizeJobFor(ctx, p.ID, c.Query("job"))
	gate, err := store.GateOptimize(ctx, s.db, user.ID, p.ID)
	if err != nil {
		s.logger.Error("evaluate optimize gate for SPA status failed", "user_id", user.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	resp := optimizeResponse{Gate: gatePayload(gate)}
	if job != nil {
		resp.Job = s.jobPayload(job)
	}
	c.JSON(http.StatusOK, resp)
}

// presetOptimizeRevert reverts to the default weights in one click: clears the three columns (store.ResetPresetWeights),
// leaves one audit record, then returns the whole list — so weight provenance, optimization time and count always come from the same database read.
func (s *Server) presetOptimizeRevert(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	p, ok := s.ownedPreset(c, user.ID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.presets.ResetPresetWeights(ctx, user.ID, p.ID); err != nil {
		s.logger.Error("reset preset weights via SPA failed", "user_id", user.ID, "preset_id", p.ID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionPresetOptimizeRevert,
		TargetType: "preset",
		TargetID:   store.Ptr(p.ID),
		Detail:     map[string]any{"via": "spa"},
	})
	s.writePresetList(c, user.ID, "return presets after SPA revert failed")
}

// presetListPayload assembles the list response: ensure the default preset exists, read the gate, and mark cards by in-flight job.
// Data fetching and gate evaluation are confined to this one method: store.EnsureDefaultPreset + one store.GateOptimize per preset,
// so a user's preset list and gates have exactly one source.
func (s *Server) presetListPayload(c *gin.Context, userID uint64) (presetListResponse, error) {
	ctx := c.Request.Context()
	presets, err := store.EnsureDefaultPreset(ctx, s.db, userID)
	if err != nil {
		return presetListResponse{}, err
	}
	// Single concurrency: at most one in-flight job globally; when it matches this preset it is attached to the card so polling can resume after a refresh.
	active, err := s.jobStore.Active(ctx)
	if err != nil {
		return presetListResponse{}, err
	}
	var activeJob *store.Job
	if active != nil && active.Kind == jobs.KindOptimize && active.TargetID != nil {
		activeJob = active
	}
	out := presetListResponse{Presets: make([]presetView, 0, len(presets))}
	for i := range presets {
		p := &presets[i]
		item := presetPayload(p)
		gate, err := store.GateOptimize(ctx, s.db, userID, p.ID)
		if err != nil {
			return presetListResponse{}, err
		}
		item.Gate = gatePayload(gate)
		if activeJob != nil && p.ID == *activeJob.TargetID {
			item.Job = s.jobPayload(activeJob)
		}
		out.Presets = append(out.Presets, item)
	}
	return out, nil
}

// writePresetList assembles and writes the list response; an assembly failure is a uniform 500. logMsg is the English diagnostic text.
func (s *Server) writePresetList(c *gin.Context, userID uint64, logMsg string) {
	resp, err := s.presetListPayload(c, userID)
	if err != nil {
		s.logger.Error(logMsg, "user_id", userID, "error", err)
		presetError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ownedPreset resolves the :id public id and confirms the preset belongs to the current user; not-owner / nonexistent / unresolvable all 404 JSON,
// so the status code never leaks the existence of someone else's preset.
func (s *Server) ownedPreset(c *gin.Context, userID uint64) (*store.Preset, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" {
		presetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	p, err := s.presets.ByPublicID(c.Request.Context(), raw)
	if err != nil {
		if !store.IsNotFound(err) {
			s.logger.Error("load preset for SPA failed", "preset_id", raw, "error", err)
			presetError(c, http.StatusInternalServerError, "internal_error")
			return nil, false
		}
		presetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	if p.OwnerUserID != userID {
		presetError(c, http.StatusNotFound, "not_found")
		return nil, false
	}
	return p, true
}

// presetPayload projects one preset into JSON; the weight criterion rests solely on whether weights_json is empty.
func presetPayload(p *store.Preset) presetView {
	item := presetView{
		ID:                  p.PublicID,
		Name:                p.Name,
		IsDefault:           p.Name == store.DefaultPresetName,
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

// gatePayload projects the gate into JSON.
func gatePayload(g store.OptimizeGate) optimizeGate {
	return optimizeGate{
		Reviews:   g.Reviews,
		Min:       g.MinReviews,
		Shortfall: g.Shortfall,
		Eligible:  g.Eligible,
	}
}

// jobPayload projects a job into JSON; result is included only when it succeeded and result_json parses,
// with a parse failure treated as "no result" and logged in English — metrics are never invented.
func (s *Server) jobPayload(job *store.Job) *jobView {
	item := &jobView{
		ID:      job.PublicID,
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
	item.Result = optimizeResultPayload(&result)
	return item
}

// optimizeResultPayload projects the fit report and gives a stable verdict (three states).
func optimizeResultPayload(r *store.OptimizeResult) *optimizeResult {
	out := &optimizeResult{
		ReviewsUsed: r.ReviewsUsed,
		Weights:     r.Weights,
		FitBefore:   fitMetricsPayload(r.FitBefore),
		FitAfter:    fitMetricsPayload(r.FitAfter),
		Verdict:     optimizeVerdict(r),
	}
	if !r.OptimizedAt.IsZero() {
		at := r.OptimizedAt.UTC().Format(time.RFC3339)
		out.OptimizedAt = &at
	}
	return out
}

// fitMetricsPayload projects the fit metrics field by field.
func fitMetricsPayload(m store.FitMetrics) fitMetrics {
	return fitMetrics{LogLoss: m.LogLoss, RMSE: m.RMSE, Items: m.Items}
}

// optimizeVerdict is the three-state criterion for comparing fits:
//   - both sides computed and the sample large enough -> improved / not_improved;
//   - both sides computed but the sample too small -> insufficient_sample;
//   - not computed -> unavailable (the frontend renders no conclusion).
func optimizeVerdict(r *store.OptimizeResult) string {
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

// parsePresetRequest parses the JSON and validates the format; a non-empty code means refused.
// Validation semantics: retention in the open interval (0,1), maximum interval a positive integer,
// steps checked against the grammar with non-positive values refused — the store's validation remains the last line of defence.
func parsePresetRequest(c *gin.Context) (presetRequest, string) {
	var req presetRequest
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
		// Boolean columns carry database defaults, so a missing field is always refused and never silently taken as false (AGENTS.md §2.3 item 9).
		return req, "invalid_request"
	}
	return req, ""
}

// applyPresetRequest writes the request fields into the preset model; EnableFuzz takes an explicit address to preserve false.
func applyPresetRequest(p *store.Preset, req presetRequest) {
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

// presetStoreCode maps the store's write validation errors to stable codes (the handler already validated before writing,
// so this is just translating the store's fallback errors into JSON codes).
func presetStoreCode(err error) string {
	switch {
	case errors.Is(err, store.ErrPresetNameRequired):
		return "preset_name_required"
	case errors.Is(err, store.ErrInvalidDesiredRetention):
		return "preset_retention_invalid"
	case errors.Is(err, store.ErrInvalidMaximumInterval):
		return "preset_max_interval_invalid"
	case errors.Is(err, store.ErrDefaultPresetCannotRename):
		// Shares the same code as the deletion guard: to the interface both mean "the default preset is protected".
		return "preset_default_protected"
	default:
		return "preset_save_failed"
	}
}

// presetError writes the preset API's error envelope; message is stable English, and the frontend maps code to a localized hint.
func presetError(c *gin.Context, status int, code string) {
	msg := presetErrorMessages[code]
	if msg == "" {
		msg = code
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}
