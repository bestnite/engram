package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the SPA scheduling preset API (internal/web/presets_api.go).
//
// Semantics match the SSR preset page item by item: default-preset backfill, the optimization gate, single-concurrency enqueueing, status and rollback all call the same set of
// store/jobs methods. Pinned here: the true values of the list and gate, persistence and auditing of create/edit, no side effects from invalid input,
// another user's preset being 404, a short gate yielding a structured shortfall, an existing job being 409, status and fit verdict after completion, rollback clearing the three columns,
// plus the two mandatory negative cases: session and CSRF.

// decodePresetList decodes the list response; failure aborts the test.
func decodePresetList(t *testing.T, rec *httptest.ResponseRecorder) presetListResponse {
	t.Helper()
	var body presetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode SPA preset list: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return body
}

// decodePresetError decodes the error envelope and the optional structured gate.
func decodePresetError(t *testing.T, rec *httptest.ResponseRecorder) (string, *optimizeGate) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Gate *optimizeGate `json:"gate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode SPA preset error: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return envelope.Error.Code, envelope.Gate
}

// findPreset picks one preset out of the list response by public id.
func findPreset(body presetListResponse, id string) *presetView {
	for i := range body.Presets {
		if body.Presets[i].ID == id {
			return &body.Presets[i]
		}
	}
	return nil
}

// TestPresetListReturnsDefaultPresetAndGate is the read acceptance: a brand-new account's GET /api/v1/presets
// yields one default preset and the user-level gate, with weight provenance "default" while unoptimized.
func TestPresetListReturnsDefaultPresetAndGate(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)

	rec := getWithCookies(t, srv, "/api/v1/presets", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/presets = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodePresetList(t, rec)
	if len(body.Presets) != 1 {
		t.Fatalf("presets = %d, want 1 (a fresh account is ensured one default preset)", len(body.Presets))
	}
	p := body.Presets[0]
	if p.Name != store.DefaultPresetName {
		t.Errorf("default preset name = %q, want %q", p.Name, store.DefaultPresetName)
	}
	if !p.IsDefault {
		t.Errorf("default preset is_default = false, want true")
	}
	if p.WeightsOptimized || p.WeightsRaw != nil || p.WeightsOptimizedAt != nil {
		t.Errorf("fresh preset reports optimised weights: %+v", p)
	}
	if p.Gate.Eligible {
		t.Errorf("gate eligible = true with no reviews, want false")
	}
	if p.Gate.Min <= 0 || p.Gate.Shortfall != int64(p.Gate.Min)-p.Gate.Reviews {
		t.Errorf("gate = %+v, want Shortfall = Min - Reviews", p.Gate)
	}
}

// TestPresetCreateAndUpdateRoundTrip covers the create and edit main path: values persist correctly, an audit record is left,
// an unticked fuzz persists as false, and the response returns the whole list.
func TestPresetCreateAndUpdateRoundTrip(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	ctx := context.Background()

	create := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets",
		`{"name":"Evening","desired_retention":0.85,"learning_steps":"2m,20m","relearning_steps":"15m","maximum_interval_days":1000,"enable_fuzz":true}`,
		cookies, csrf)
	if create.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/presets = %d, want 200 (body %s)", create.Code, snippet(create.Body.String()))
	}
	created := decodePresetList(t, create)
	var evening *presetView
	for i := range created.Presets {
		if created.Presets[i].Name == "Evening" {
			evening = &created.Presets[i]
		}
	}
	if evening == nil {
		t.Fatalf("created preset missing from response: %s", snippet(create.Body.String()))
	}
	if evening.IsDefault {
		t.Errorf("created preset reports is_default = true, want false")
	}
	var persisted store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "Evening").First(&persisted).Error; err != nil {
		t.Fatalf("created preset not persisted: %v", err)
	}
	if persisted.DesiredRetention != 0.85 || persisted.LearningSteps != "2m,20m" ||
		persisted.RelearningSteps != "15m" || persisted.MaximumIntervalDays != 1000 || !persisted.FuzzEnabled() {
		t.Errorf("persisted preset = %+v, want 0.85 / 2m,20m / 15m / 1000 / fuzz on", persisted)
	}

	upd := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+evening.ID,
		`{"name":"Night","desired_retention":0.8,"learning_steps":"5m","relearning_steps":"","maximum_interval_days":500,"enable_fuzz":false}`,
		cookies, csrf)
	if upd.Code != http.StatusOK {
		t.Fatalf("PATCH /api/v1/presets/%s = %d, want 200 (body %s)", evening.ID, upd.Code, snippet(upd.Body.String()))
	}
	reloaded, err := store.NewPresetStore(db).ByPublicID(ctx, evening.ID)
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

// TestPresetWriteRejectsInvalidInput is a mandatory negative case: every kind of invalid input is 400 + a stable code,
// with nothing written (the preset total stays 0).
func TestPresetWriteRejectsInvalidInput(t *testing.T) {
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
			if code, _ := decodePresetError(t, rec); code != tc.wantCode {
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

// TestPresetUpdateOtherUsersPresetIsNotFound is a mandatory negative case: another user's preset returns 404 (leaking no existence),
// and the column values are unchanged.
func TestPresetUpdateOtherUsersPresetIsNotFound(t *testing.T) {
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

	rec := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+foreign.PublicID,
		`{"name":"hijack","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`,
		cookies, csrf)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH another user's preset = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if code, _ := decodePresetError(t, rec); code != "not_found" {
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

// TestPresetOptimizeGateAndConflict covers a short gate and single concurrency: when short, 400 with a structured shortfall and
// no job created; when the gate is met, 202 creates a job; the second attempt is 409 and creates nothing more.
func TestPresetOptimizeGateAndConflict(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	path := "/api/v1/presets/" + p.PublicID + "/optimize"

	below := jsonRequest(t, srv, http.MethodPost, path, `{}`, cookies, csrf)
	if below.Code != http.StatusBadRequest {
		t.Fatalf("POST optimize below threshold = %d, want 400 (body %s)", below.Code, snippet(below.Body.String()))
	}
	code, gate := decodePresetError(t, below)
	if code != "insufficient_reviews" {
		t.Errorf("below-threshold code = %q, want insufficient_reviews", code)
	}
	if gate == nil || gate.Eligible || gate.Shortfall <= 0 || gate.Min <= 0 {
		t.Errorf("below-threshold gate = %+v, want an ineligible gate with a positive shortfall", gate)
	}

	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, p.ID, store.MinOptimizeMinReviews)

	first := jsonRequest(t, srv, http.MethodPost, path, `{}`, cookies, csrf)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first eligible optimize = %d, want 202 (body %s)", first.Code, snippet(first.Body.String()))
	}
	var body optimizeResponse
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
	if code, _ := decodePresetError(t, second); code != "optimize_conflict" {
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

// TestPresetOptimizeStatusAndRevert covers status after completion, the fit verdict and one-click rollback:
// the in-flight job in the list can resume polling, status returns succeeded + a three-state verdict, and rollback clears the three columns back to the default weights.
func TestPresetOptimizeStatusAndRevert(t *testing.T) {
	srv, db, ownerID, cookies, csrf, runner := newPresetsServer(t)
	ctx := context.Background()
	p := seedPreset(t, db, ownerID)
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, p.ID, store.MinOptimizeMinReviews)

	enq := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+p.PublicID+"/optimize", `{}`, cookies, csrf)
	if enq.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d, want 202 (body %s)", enq.Code, snippet(enq.Body.String()))
	}

	// The in-flight job must be marked in the list so polling can resume after a page refresh.
	list := decodePresetList(t, getWithCookies(t, srv, "/api/v1/presets", cookies))
	card := findPreset(list, p.PublicID)
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

	status := getWithCookies(t, srv, "/api/v1/presets/"+p.PublicID+"/optimize/status?job="+job.PublicID, cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", status.Code, snippet(status.Body.String()))
	}
	var statusBody optimizeResponse
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

	revert := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+p.PublicID+"/optimize/revert", `{}`, cookies, csrf)
	if revert.Code != http.StatusOK {
		t.Fatalf("POST revert = %d, want 200 (body %s)", revert.Code, snippet(revert.Body.String()))
	}
	after := decodePresetList(t, revert)
	reverted := findPreset(after, p.PublicID)
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

// TestPresetOptimizeStatusIgnoresForeignJob is a mandatory negative case: the status endpoint only recognises this preset's jobs,
// and a job id pointing at another preset is always answered as "no job", leaking nothing about it.
func TestPresetOptimizeStatusIgnoresForeignJob(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p1 := seedPreset(t, db, ownerID)
	p2 := store.NewPreset(ownerID, "Other")
	if err := store.NewPresetStore(db).Create(context.Background(), &p2); err != nil {
		t.Fatalf("create second preset: %v", err)
	}
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, p1.ID, store.MinOptimizeMinReviews)

	enq := jsonRequest(t, srv, http.MethodPost, "/api/v1/presets/"+p1.PublicID+"/optimize", `{}`, cookies, csrf)
	if enq.Code != http.StatusAccepted {
		t.Fatalf("enqueue = %d, want 202", enq.Code)
	}
	var job store.Job
	if err := db.Order("id desc").First(&job).Error; err != nil {
		t.Fatalf("load job: %v", err)
	}

	status := getWithCookies(t, srv, "/api/v1/presets/"+p2.PublicID+"/optimize/status?job="+job.PublicID, cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status for the other preset = %d, want 200", status.Code)
	}
	var body optimizeResponse
	if err := json.Unmarshal(status.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if body.Job != nil {
		t.Errorf("status returned another preset's job: %+v", body.Job)
	}
}

// TestPresetsRequireSessionAndCSRF is a mandatory negative case: anonymous GET is 401, a write without CSRF is 403,
// and neither produces side effects.
func TestPresetsRequireSessionAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)

	anon := getWithCookies(t, srv, "/api/v1/presets", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", anon.Code, snippet(anon.Body.String()))
	}
	if code, _ := decodePresetError(t, anon); code != "unauthorized" {
		t.Errorf("anonymous code = %q, want unauthorized", code)
	}

	createBody := `{"name":"No CSRF","desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true}`
	for _, target := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/presets", createBody},
		{http.MethodPatch, "/api/v1/presets/" + p.PublicID, createBody},
		{http.MethodDelete, "/api/v1/presets/" + p.PublicID, ""},
		{http.MethodPost, "/api/v1/presets/" + p.PublicID + "/optimize", `{}`},
		{http.MethodPost, "/api/v1/presets/" + p.PublicID + "/optimize/revert", `{}`},
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

// TestPresetDelete covers deleting a preset through the SPA endpoint: default protection, reference protection, successful deletion.
func TestPresetDelete(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	ctx := context.Background()

	// 1. Try to delete the always-present default preset
	defaultPresets, err := store.EnsureDefaultPreset(ctx, db, ownerID)
	if err != nil {
		t.Fatalf("ensure default preset: %v", err)
	}
	delDefault := jsonRequest(t, srv, http.MethodDelete, "/api/v1/presets/"+defaultPresets[0].PublicID, "", cookies, csrf)
	if delDefault.Code != http.StatusBadRequest {
		t.Errorf("DELETE default preset = %d, want 400 (body %s)", delDefault.Code, snippet(delDefault.Body.String()))
	}

	// 2. Create a custom preset
	custom := store.NewPreset(ownerID, "Custom For Delete")
	if err := store.NewPresetStore(db).Create(ctx, &custom); err != nil {
		t.Fatalf("create custom preset: %v", err)
	}

	// 3. Deletion is refused once a deck references it (409 Conflict)
	d := &store.Deck{OwnerUserID: ownerID, Name: "Deck using preset", PresetID: custom.ID}
	if err := store.NewDeckStore(db).Create(ctx, d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	delInUse := jsonRequest(t, srv, http.MethodDelete, "/api/v1/presets/"+custom.PublicID, "", cookies, csrf)
	if delInUse.Code != http.StatusConflict {
		t.Errorf("DELETE in-use preset = %d, want 409 (body %s)", delInUse.Code, snippet(delInUse.Body.String()))
	}

	// 4. Deletion succeeds after the reference is cleared (deck deleted)
	if err := store.NewDeckStore(db).Delete(ctx, ownerID, d.ID); err != nil {
		t.Fatalf("delete deck: %v", err)
	}
	delOk := jsonRequest(t, srv, http.MethodDelete, "/api/v1/presets/"+custom.PublicID, "", cookies, csrf)
	if delOk.Code != http.StatusOK {
		t.Fatalf("DELETE unused preset = %d, want 200 (body %s)", delOk.Code, snippet(delOk.Body.String()))
	}

	// Confirm the row is gone from the database
	if _, err := store.NewPresetStore(db).ByID(ctx, custom.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("preset %d still in db after delete: %v", custom.ID, err)
	}
}

// TestPresetDefaultCannotBeRenamed asserts renaming the default preset is refused at the HTTP layer (400 + a stable code, with the
// stored name unchanged), while sending the name back verbatim and changing the parameters still succeeds.
// The interface's "Default" display name relies on this name as its identity marker: once changed, a later backfill would create another preset with that name.
func TestPresetDefaultCannotBeRenamed(t *testing.T) {
	srv, db, _, cookies, csrf, _ := newPresetsServer(t)
	ctx := context.Background()

	body := decodePresetList(t, getWithCookies(t, srv, "/api/v1/presets", cookies))
	var def *presetView
	for i := range body.Presets {
		if body.Presets[i].IsDefault {
			def = &body.Presets[i]
		}
	}
	if def == nil {
		t.Fatal("no preset reports is_default = true")
	}

	params := `{"name":%q,"desired_retention":0.85,"learning_steps":"1m,10m","relearning_steps":"10m","maximum_interval_days":36500,"enable_fuzz":true}`

	renamed := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+def.ID,
		fmt.Sprintf(params, "Renamed Default"), cookies, csrf)
	if renamed.Code != http.StatusBadRequest {
		t.Fatalf("PATCH rename default = %d, want 400 (body %s)", renamed.Code, snippet(renamed.Body.String()))
	}
	if code, _ := decodePresetError(t, renamed); code != "preset_default_protected" {
		t.Errorf("rename default code = %q, want preset_default_protected", code)
	}
	var persisted store.Preset
	if err := db.First(&persisted, "public_id = ?", def.ID).Error; err != nil {
		t.Fatalf("reload default preset: %v", err)
	}
	if persisted.Name != store.DefaultPresetName {
		t.Errorf("default preset name after rejected rename = %q, want %q", persisted.Name, store.DefaultPresetName)
	}

	edited := jsonRequest(t, srv, http.MethodPatch, "/api/v1/presets/"+def.ID,
		fmt.Sprintf(params, store.DefaultPresetName), cookies, csrf)
	if edited.Code != http.StatusOK {
		t.Fatalf("PATCH default params = %d, want 200 (body %s)", edited.Code, snippet(edited.Body.String()))
	}
	reloaded, err := store.NewPresetStore(db).ByPublicID(ctx, def.ID)
	if err != nil {
		t.Fatalf("reload default preset: %v", err)
	}
	if reloaded.Name != store.DefaultPresetName || reloaded.DesiredRetention != 0.85 {
		t.Errorf("default preset after param edit = %+v, want name %q and retention 0.85",
			reloaded, store.DefaultPresetName)
	}
}

// TestPresetGateCountsOnlyItsOwnDecks asserts each preset's gate in the list counts only reviews on decks carrying it:
// with two presets on one account and reviews only on the first preset's deck, the second preset's usable count is 0 (the negative side).
func TestPresetGateCountsOnlyItsOwnDecks(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	used := seedPreset(t, db, ownerID)
	idle := store.NewPreset(ownerID, "Idle")
	if err := store.NewPresetStore(db).Create(context.Background(), &idle); err != nil {
		t.Fatalf("create idle preset: %v", err)
	}
	seedReviews(t, db, ownerID, used.ID, 7)

	body := decodePresetList(t, getWithCookies(t, srv, "/api/v1/presets", cookies))
	got := map[string]int64{}
	for _, p := range body.Presets {
		got[p.ID] = p.Gate.Reviews
	}
	if got[used.PublicID] != 7 || got[idle.PublicID] != 0 {
		t.Errorf("gate reviews = %v, want %s:7 and %s:0", got, used.PublicID, idle.PublicID)
	}
}
