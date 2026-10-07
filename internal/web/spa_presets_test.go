package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 SPA 调度预设接口（DESIGN.md §3.5、§8.1，internal/web/spa_presets.go）。
//
// 语义与 SSR 预设页逐项一致：默认预设补齐、优化门槛、单并发入队、状态与回退都调用同一批
// store/jobs 方法。这里钉住：列表与门槛的真值、创建/编辑的落库与审计、非法输入零副作用、
// 他人预设 404、门槛不足给出结构化差额、已有作业 409、完成后状态与拟合结论、回退清三列，
// 以及会话 + CSRF 两个必测负例。

// decodeSPAPresetList 解析列表响应；失败即终止测试。
func decodeSPAPresetList(t *testing.T, rec *httptest.ResponseRecorder) spaPresetListResponse {
	t.Helper()
	var body spaPresetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode SPA preset list: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// decodeSPAPresetError 解析错误包壳与可选的结构化门槛。
func decodeSPAPresetError(t *testing.T, rec *httptest.ResponseRecorder) (string, *spaOptimizeGate) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Gate *spaOptimizeGate `json:"gate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode SPA preset error: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return envelope.Error.Code, envelope.Gate
}

// findSPAPreset 在列表响应里按 id 取一条预设。
func findSPAPreset(body spaPresetListResponse, id uint64) *spaPreset {
	for i := range body.Presets {
		if body.Presets[i].ID == id {
			return &body.Presets[i]
		}
	}
	return nil
}

// TestSPAPresetListReturnsDefaultPresetAndGate 是读验收：全新账号 GET /api/v1/presets
// 得到一个默认预设与用户级门槛，未优化时权重来源是默认。
func TestSPAPresetListReturnsDefaultPresetAndGate(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)

	rec := getWithCookies(t, srv, "/api/v1/presets", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/presets = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeSPAPresetList(t, rec)
	if len(body.Presets) != 1 {
		t.Fatalf("presets = %d, want 1 (a fresh account is ensured one default preset)", len(body.Presets))
	}
	p := body.Presets[0]
	if p.Name != store.DefaultPresetName {
		t.Errorf("default preset name = %q, want %q", p.Name, store.DefaultPresetName)
	}
	if p.WeightsOptimized || p.WeightsRaw != nil || p.WeightsOptimizedAt != nil {
		t.Errorf("fresh preset reports optimised weights: %+v", p)
	}
	if body.Gate.Eligible {
		t.Errorf("gate eligible = true with no reviews, want false")
	}
	if body.Gate.Min <= 0 || body.Gate.Shortfall != int64(body.Gate.Min)-body.Gate.Reviews {
		t.Errorf("gate = %+v, want Shortfall = Min - Reviews", body.Gate)
	}
}

// TestSPAPresetCreateAndUpdateRoundTrip 覆盖创建与编辑主路径：值正确落库、留审计、
// 未勾选 fuzz 落库为 false，且响应回整份列表。
func TestSPAPresetCreateAndUpdateRoundTrip(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	ctx := context.Background()

	create := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets",
		`{"name":"Evening","desired_retention":0.85,"learning_steps":"2m,20m","relearning_steps":"15m","maximum_interval_days":1000,"enable_fuzz":true}`,
		cookies, csrf)
	if create.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/presets = %d, want 200 (body %s)", create.Code, snippet(create.Body.String()))
	}
	created := decodeSPAPresetList(t, create)
	var evening *spaPreset
	for i := range created.Presets {
		if created.Presets[i].Name == "Evening" {
			evening = &created.Presets[i]
		}
	}
	if evening == nil {
		t.Fatalf("created preset missing from response: %s", snippet(create.Body.String()))
	}
	var persisted store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "Evening").First(&persisted).Error; err != nil {
		t.Fatalf("created preset not persisted: %v", err)
	}
	if persisted.DesiredRetention != 0.85 || persisted.LearningSteps != "2m,20m" ||
		persisted.RelearningSteps != "15m" || persisted.MaximumIntervalDays != 1000 || !persisted.FuzzEnabled() {
		t.Errorf("persisted preset = %+v, want 0.85 / 2m,20m / 15m / 1000 / fuzz on", persisted)
	}

	upd := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+u64str(evening.ID),
		`{"name":"Night","desired_retention":0.8,"learning_steps":"5m","relearning_steps":"","maximum_interval_days":500,"enable_fuzz":false}`,
		cookies, csrf)
	if upd.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/presets/%d = %d, want 200 (body %s)", evening.ID, upd.Code, snippet(upd.Body.String()))
	}
	reloaded, err := store.NewPresetStore(db).ByID(ctx, evening.ID)
	if err != nil {
		t.Fatalf("reload preset: %v", err)
	}
	if reloaded.Name != "Night" || reloaded.DesiredRetention != 0.8 || reloaded.LearningSteps != "5m" ||
		reloaded.RelearningSteps != "" || reloaded.MaximumIntervalDays != 500 || reloaded.FuzzEnabled() {
		t.Errorf("updated preset = %+v, want Night / 0.8 / 5m / empty / 500 / fuzz off", reloaded)
	}
	audit := store.NewAuditStore(db)
	for action, want := range map[string]int64{store.ActionPresetCreate: 1, store.ActionPresetUpdate: 1} {
		n, err := audit.CountByAction(ctx, action)
		if err != nil {
			t.Fatalf("count audit %s: %v", action, err)
		}
		if n != want {
			t.Errorf("audit rows for %s = %d, want %d", action, n, want)
		}
	}
}

// TestSPAPresetWriteRejectsInvalidInput 是必测负例：各类非法输入一律 400 + 稳定 code，
// 且不写库（预设总数保持 0）。
func TestSPAPresetWriteRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"empty name", `{"name":"  ","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_name_required"},
		{"retention zero", `{"name":"x","desired_retention":0,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_retention_invalid"},
		{"retention one", `{"name":"x","desired_retention":1,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_retention_invalid"},
		{"retention above one", `{"name":"x","desired_retention":1.5,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_retention_invalid"},
		{"max interval zero", `{"name":"x","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":0,"enable_fuzz":true}`, "preset_max_interval_invalid"},
		{"negative steps", `{"name":"x","desired_retention":0.9,"learning_steps":"-5m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_steps_invalid"},
		{"unparsable steps", `{"name":"x","desired_retention":0.9,"learning_steps":"1m,zzz","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`, "preset_steps_invalid"},
		{"missing enable_fuzz", `{"name":"x","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100}`, "invalid_request"},
		{"bad json", `{`, "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
			rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets", tc.body, cookies, csrf)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST %s = %d, want 400 (body %s)", tc.body, rec.Code, snippet(rec.Body.String()))
			}
			if code, _ := decodeSPAPresetError(t, rec); code != tc.wantCode {
				t.Errorf("error code = %q, want %q", code, tc.wantCode)
			}
			var n int64
			if err := db.Model(&store.Preset{}).Where("owner_user_id = ?", ownerID).Count(&n).Error; err != nil {
				t.Fatalf("count presets: %v", err)
			}
			if n != 0 {
				t.Errorf("presets = %d after a rejected write, want 0", n)
			}
		})
	}
}

// TestSPAPresetUpdateOtherUsersPresetIsNotFound 是必测负例：他人预设返回 404（不泄露存在性），
// 且列值不变。
func TestSPAPresetUpdateOtherUsersPresetIsNotFound(t *testing.T) {
	srv, db, _, cookies, csrf, _ := newPresetsServer(t)
	ctx := context.Background()
	other := store.User{
		Username: "intruder", Email: "intruder@example.com", DisplayName: "intruder",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "zh-CN",
		Timezone: "Asia/Shanghai", CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(db).Create(ctx, &other); err != nil {
		t.Fatalf("create second user: %v", err)
	}
	foreign := store.NewPreset(other.ID, "foreign")
	if err := store.NewPresetStore(db).Create(ctx, &foreign); err != nil {
		t.Fatalf("create foreign preset: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+u64str(foreign.ID),
		`{"name":"hijack","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`,
		cookies, csrf)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH another user's preset = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if code, _ := decodeSPAPresetError(t, rec); code != "not_found" {
		t.Errorf("error code = %q, want not_found", code)
	}
	after, err := store.NewPresetStore(db).ByID(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("reload foreign preset: %v", err)
	}
	if after.Name != "foreign" {
		t.Errorf("foreign preset name = %q, want unchanged", after.Name)
	}
}

// TestSPAPresetOptimizeGateAndConflict 覆盖门槛不足与单并发：不足时 400 且给出结构化差额、
// 不建作业；够门槛时 202 建作业；第二次 409 且不再建。
func TestSPAPresetOptimizeGateAndConflict(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	path := "/api/v1/presets/" + u64str(p.ID) + "/optimize"

	below := jsonRequest(t, srv, http.MethodPost, path, `{}`, cookies, csrf)
	if below.Code != http.StatusBadRequest {
		t.Fatalf("POST optimize below threshold = %d, want 400 (body %s)", below.Code, snippet(below.Body.String()))
	}
	code, gate := decodeSPAPresetError(t, below)
	if code != "insufficient_reviews" {
		t.Errorf("below-threshold code = %q, want insufficient_reviews", code)
	}
	if gate == nil || gate.Eligible || gate.Shortfall <= 0 || gate.Min <= 0 {
		t.Errorf("below-threshold gate = %+v, want an ineligible gate with a positive shortfall", gate)
	}

	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, store.MinOptimizeMinReviews)

	first := jsonRequest(t, srv, http.MethodPost, path, `{}`, cookies, csrf)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first eligible optimize = %d, want 202 (body %s)", first.Code, snippet(first.Body.String()))
	}
	var body spaOptimizeResponse
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode optimize response: %v", err)
	}
	if body.Job == nil || body.Job.Status != "queued" {
		t.Errorf("optimize job = %+v, want a queued job", body.Job)
	}

	second := jsonRequest(t, srv, http.MethodPost, path, `{}`, cookies, csrf)
	if second.Code != http.StatusConflict {
		t.Fatalf("second optimize = %d, want 409 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if code, _ := decodeSPAPresetError(t, second); code != "optimize_conflict" {
		t.Errorf("conflict code = %q, want optimize_conflict", code)
	}
	var jobs int64
	if err := db.Model(&store.Job{}).Count(&jobs).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 1 {
		t.Errorf("jobs = %d, want 1 after a rejected second enqueue", jobs)
	}
}

// TestSPAPresetOptimizeStatusAndRevert 覆盖完成后状态、拟合结论与一键回退：
// 列表在途作业可续轮询、状态返回 succeeded + 三态结论、回退清三列并回默认权重。
func TestSPAPresetOptimizeStatusAndRevert(t *testing.T) {
	srv, db, ownerID, cookies, csrf, runner := newPresetsServer(t)
	ctx := context.Background()
	p := seedPreset(t, db, ownerID)
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, store.MinOptimizeMinReviews)

	enq := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+u64str(p.ID)+"/optimize", `{}`, cookies, csrf)
	if enq.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d, want 202 (body %s)", enq.Code, snippet(enq.Body.String()))
	}

	// 在途作业必须在列表里标注，刷新页面后才能续上轮询。
	list := decodeSPAPresetList(t, getWithCookies(t, srv, "/api/v1/presets", cookies))
	card := findSPAPreset(list, p.ID)
	if card == nil || card.Job == nil || card.Job.Status != "queued" {
		t.Fatalf("in-flight job not attached to the preset card: %+v", card)
	}

	var job store.Job
	if err := db.Order("id desc").First(&job).Error; err != nil {
		t.Fatalf("load enqueued job: %v", err)
	}
	weights := make([]float64, 21)
	for i := range weights {
		weights[i] = float64(i) + 0.5
	}
	result := store.OptimizeResult{
		ReviewsUsed: store.MinOptimizeMinReviews,
		Weights:     weights,
		FitBefore:   store.FitMetrics{LogLoss: 0.51, RMSE: 0.42, Items: store.MinOptimizeMinReviews},
		FitAfter:    store.FitMetrics{LogLoss: 0.44, RMSE: 0.38, Items: store.MinOptimizeMinReviews},
		OptimizedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	if err := runner.FinishOptimize(ctx, job.ID, result, "training done"); err != nil {
		t.Fatalf("FinishOptimize() error = %v", err)
	}

	status := getWithCookies(t, srv, "/api/v1/presets/"+u64str(p.ID)+"/optimize/status?job="+strconv.FormatUint(job.ID, 10), cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", status.Code, snippet(status.Body.String()))
	}
	var statusBody spaOptimizeResponse
	if err := json.Unmarshal(status.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if statusBody.Job == nil || statusBody.Job.Status != "succeeded" {
		t.Fatalf("status job = %+v, want succeeded", statusBody.Job)
	}
	if statusBody.Job.Result == nil {
		t.Fatal("succeeded job is missing its result summary")
	}
	if statusBody.Job.Result.Verdict != "improved" {
		t.Errorf("verdict = %q, want improved (0.44 < 0.51 with a sufficient sample)", statusBody.Job.Result.Verdict)
	}
	if statusBody.Job.Result.FitBefore.LogLoss != 0.51 || statusBody.Job.Result.FitAfter.LogLoss != 0.44 {
		t.Errorf("fit metrics = %+v, want before 0.51 / after 0.44", statusBody.Job.Result)
	}

	revert := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+u64str(p.ID)+"/optimize/revert", `{}`, cookies, csrf)
	if revert.Code != http.StatusOK {
		t.Fatalf("POST revert = %d, want 200 (body %s)", revert.Code, snippet(revert.Body.String()))
	}
	after := decodeSPAPresetList(t, revert)
	reverted := findSPAPreset(after, p.ID)
	if reverted == nil || reverted.WeightsOptimized || reverted.WeightsRaw != nil {
		t.Errorf("revert response still reports optimised weights: %+v", reverted)
	}
	stored, err := store.NewPresetStore(db).ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("reload preset after revert: %v", err)
	}
	if stored.WeightsJSON != nil || stored.WeightsOptimizedAt != nil || stored.WeightsReviewCount != nil {
		t.Errorf("weights after revert = (%v, %v, %v), want all NULL",
			stored.WeightsJSON, stored.WeightsOptimizedAt, stored.WeightsReviewCount)
	}
}

// TestSPAPresetOptimizeStatusIgnoresForeignJob 是必测负例：状态端点只认本预设的作业，
// 指向别的预设的 job id 一律按「无作业」返回，不泄露它。
func TestSPAPresetOptimizeStatusIgnoresForeignJob(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p1 := seedPreset(t, db, ownerID)
	p2 := store.NewPreset(ownerID, "Other")
	if err := store.NewPresetStore(db).Create(context.Background(), &p2); err != nil {
		t.Fatalf("create second preset: %v", err)
	}
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, store.MinOptimizeMinReviews)

	enq := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+u64str(p1.ID)+"/optimize", `{}`, cookies, csrf)
	if enq.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d, want 202", enq.Code)
	}
	var job store.Job
	if err := db.Order("id desc").First(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}

	status := getWithCookies(t, srv, "/api/v1/presets/"+u64str(p2.ID)+"/optimize/status?job="+strconv.FormatUint(job.ID, 10), cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status for the other preset = %d, want 200", status.Code)
	}
	var body spaOptimizeResponse
	if err := json.Unmarshal(status.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Job != nil {
		t.Errorf("status returned another preset's job: %+v", body.Job)
	}
}

// TestSPAPresetsRequireSessionAndCSRF 是必测负例：匿名 GET 401、缺 CSRF 的写操作 403，
// 且都不产生副作用。
func TestSPAPresetsRequireSessionAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)

	anon := getWithCookies(t, srv, "/api/v1/presets", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	if code, _ := decodeSPAPresetError(t, anon); code != "unauthorized" {
		t.Errorf("anonymous code = %q, want unauthorized", code)
	}

	createBody := `{"name":"No CSRF","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`
	for _, target := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/presets", createBody},
		{http.MethodPatch, "/api/v1/presets/" + u64str(p.ID), createBody},
		{http.MethodPost, "/api/v1/presets/" + u64str(p.ID) + "/optimize", `{}`},
		{http.MethodPost, "/api/v1/presets/" + u64str(p.ID) + "/optimize/revert", `{}`},
	} {
		rec := jsonRequest(t, srv, target.method, target.path, target.body, cookies, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF = %d, want 403 (body %s)", target.method, target.path, rec.Code, snippet(rec.Body.String()))
		}
	}

	var presets int64
	if err := db.Model(&store.Preset{}).Where("owner_user_id = ?", ownerID).Count(&presets).Error; err != nil {
		t.Fatalf("count presets: %v", err)
	}
	if presets != 1 {
		t.Errorf("presets = %d after CSRF-less writes, want 1", presets)
	}
}
