package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// newPresetsServer 装配一个带作业执行器的测试服务并登录 owner。
// runner 不调用 Start：作业会停在内存队列里保持 queued，测试因此可以确定性地模拟
// 「适配器跑完并写回权重」，而不用真的跑外部优化器（M9-4 允许注入假命令/直接驱动状态）。
func newPresetsServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string, runner *jobs.Runner) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "presets.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	runner, err = jobs.New(jobs.Deps{DB: db, Logger: discardLogger()})
	if err != nil {
		t.Fatalf("jobs.New() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		Jobs:          runner,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	login := postForm(t, srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", login.Code)
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	return srv, db, owner.ID, login.Result().Cookies(), sess.CSRFToken, runner
}

// seedPreset 建一个属于 owner 的**默认**预设：名用稳定字面量 store.DefaultPresetName，
// 这样页面渲染时的 store.EnsureDefaultPreset 会认它、不再补一条（M3-14）。
func seedPreset(t *testing.T, db *gorm.DB, ownerID uint64) *store.Preset {
	t.Helper()
	p := store.NewPreset(ownerID, store.DefaultPresetName)
	if err := store.NewPresetStore(db).Create(context.Background(), &p); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return &p
}

// seedReviews 插入 n 条属于 userID 的复习日志；优化门槛只按 user_id 计数。
func seedReviews(t *testing.T, db *gorm.DB, userID uint64, n int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		row := store.Review{
			CardID:      uint64(i + 1),
			UserID:      userID,
			Rating:      3,
			GradeSource: "self",
			ReviewedAt:  now,
			ReviewDay:   now.Format("2006-01-02"),
			StateBefore: 2,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}
}

// setMinReviews 覆盖优化门槛；put 的值是 JSON 编码字符串，optimize 读取端再解码。
func setMinReviews(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	if err := store.PutSetting(context.Background(), db, store.SettingKeyOptimizeMinReviews,
		strconv.Itoa(n), nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(%s): %v", store.SettingKeyOptimizeMinReviews, err)
	}
}

// TestPresetOptimizeEnqueueAndConflict 覆盖 M9-4 的触发与单并发 409：
// 入队成功返回 202 并带轮询卡片；第二次入队返回 409 且不再建作业。
func TestPresetOptimizeEnqueueAndConflict(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	// M9-12 门槛下限 300：入队成功需要真够 300 条复习。
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, store.MinOptimizeMinReviews)

	first := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize",
		url.Values{"csrf_token": {csrf}}, cookies)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first POST optimize status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	body := first.Body.String()
	if !strings.Contains(body, "已入队") {
		t.Errorf("202 body does not show the queued state: %s", body)
	}
	if !strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Errorf("202 body does not poll while the job is in flight: %s", body)
	}

	second := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize",
		url.Values{"csrf_token": {csrf}}, cookies)
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST optimize status = %d, want 409 (body %s)", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "已有优化任务正在运行") {
		t.Errorf("409 body does not explain the conflict: %s", second.Body.String())
	}
	var count int64
	if err := db.Model(&store.Job{}).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Errorf("jobs = %d after a rejected second enqueue, want 1", count)
	}
}

// TestPresetOptimizeShortfallNamesTheGap 覆盖 M9-4 的「还差 N 条」：不足门槛时返回 400，
// 页面必须给出还差多少条，而不是只说失败。
func TestPresetOptimizeShortfallNamesTheGap(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	// 默认门槛 500，0 条复习 → 还差 500 条。

	rec := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize",
		url.Values{"csrf_token": {csrf}}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST optimize below threshold status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "还差 500 条") {
		t.Errorf("shortfall body does not name the missing count: %s", rec.Body.String())
	}
	var count int64
	if err := db.Model(&store.Job{}).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Errorf("jobs = %d below threshold, want 0", count)
	}
}

// TestPresetOptimizeCompletionShowsDatabaseWeights 覆盖 M9-4 主验收：
// 完成后页面显示的权重与库里一致；一键回退后 weights 归 NULL 且页面回到默认权重。
func TestPresetOptimizeCompletionShowsDatabaseWeights(t *testing.T) {
	srv, db, ownerID, cookies, csrf, runner := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	// M9-12 门槛下限 300：入队成功需要真够 300 条复习。
	setMinReviews(t, db, store.MinOptimizeMinReviews)
	seedReviews(t, db, ownerID, store.MinOptimizeMinReviews)

	rec := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize",
		url.Values{"csrf_token": {csrf}}, cookies)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST optimize status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	var job store.Job
	if err := db.Order("id desc").First(&job).Error; err != nil {
		t.Fatalf("load enqueued job: %v", err)
	}

	// 模拟适配器跑完：写 21 维权重到预设，并把拟合报告写进 job 行（M9-2/M9-5 的契约）。
	weights := make([]float64, 21)
	for i := range weights {
		weights[i] = float64(i) + 0.5
	}
	raw, err := json.Marshal(weights)
	if err != nil {
		t.Fatalf("marshal weights: %v", err)
	}
	encoded := string(raw)
	optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	reviewCount := 600
	p.WeightsJSON = &encoded
	p.WeightsOptimizedAt = &optimizedAt
	p.WeightsReviewCount = &reviewCount
	if err := store.NewPresetStore(db).Update(context.Background(), ownerID, p); err != nil {
		t.Fatalf("write optimized weights: %v", err)
	}
	result := store.OptimizeResult{
		ReviewsUsed: 600,
		Weights:     weights,
		// Items 必须 >= store.MinFitItems（M9-12），否则页面改渲染「样本不足」而不给结论。
		FitBefore:   store.FitMetrics{LogLoss: 0.51, RMSE: 0.42, Items: reviewCount},
		FitAfter:    store.FitMetrics{LogLoss: 0.44, RMSE: 0.38, Items: reviewCount},
		OptimizedAt: optimizedAt,
	}
	if err := runner.FinishOptimize(context.Background(), job.ID, result, "training done"); err != nil {
		t.Fatalf("FinishOptimize() error = %v", err)
	}

	// 轮询状态端点：完成后不再轮询，卡片摘要必须来自数据库。
	status := getWithCookies(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize/status?job="+strconv.FormatUint(job.ID, 10), cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", status.Code, status.Body.String())
	}
	page := status.Body.String()
	if !strings.Contains(page, "已优化") {
		t.Errorf("status body does not show the optimised weight source: %s", page)
	}
	// 与库一致：页面里的权重明细等于数据库里那一列。
	got, err := store.NewPresetStore(db).ByID(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("reload preset: %v", err)
	}
	if got.WeightsJSON == nil {
		t.Fatal("weights_json is nil in the database after the simulated adapter run")
	}
	if !strings.Contains(page, *got.WeightsJSON) {
		t.Errorf("page weights %q != database weights %q", page, *got.WeightsJSON)
	}
	if !strings.Contains(page, "600") {
		t.Errorf("status body does not show the review count used: %s", page)
	}
	if !strings.Contains(page, "优化完成") || !strings.Contains(page, "拟合对比") {
		t.Errorf("status body does not show the completion summary: %s", page)
	}
	if !strings.Contains(page, "0.4400") || !strings.Contains(page, "0.5100") {
		t.Errorf("status body does not show the before/after fit: %s", page)
	}

	// 一键回退：三列归 NULL，页面回到默认权重。
	revert := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize/revert",
		url.Values{"csrf_token": {csrf}}, cookies)
	if revert.Code != http.StatusOK {
		t.Fatalf("POST revert status = %d, want 200 (body %s)", revert.Code, revert.Body.String())
	}
	if !strings.Contains(revert.Body.String(), "默认权重") {
		t.Errorf("revert body does not show the default weight source: %s", revert.Body.String())
	}
	if strings.Contains(revert.Body.String(), *got.WeightsJSON) {
		t.Errorf("revert body still shows the optimised weights: %s", revert.Body.String())
	}
	after, err := store.NewPresetStore(db).ByID(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("reload preset after revert: %v", err)
	}
	if after.WeightsJSON != nil || after.WeightsOptimizedAt != nil || after.WeightsReviewCount != nil {
		t.Errorf("weights after revert = (%v, %v, %v), want all NULL",
			after.WeightsJSON, after.WeightsOptimizedAt, after.WeightsReviewCount)
	}

	// 写操作必须留痕（AGENTS.md §2.5）：触发一行、回退一行。
	audit := store.NewAuditStore(db)
	for action, want := range map[string]int64{store.ActionPresetOptimize: 1, store.ActionPresetOptimizeRevert: 1} {
		n, err := audit.CountByAction(context.Background(), action)
		if err != nil {
			t.Fatalf("count audit %s: %v", action, err)
		}
		if n != want {
			t.Errorf("audit rows for %s = %d, want %d", action, n, want)
		}
	}
}

// TestPresetOptimizeRequiresCSRF 是必测负例：缺 CSRF token 的触发被拒且不建作业。
func TestPresetOptimizeRequiresCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	rec := postForm(t, srv, "/presets/"+strconv.FormatUint(p.ID, 10)+"/optimize", url.Values{}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST optimize without CSRF status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	var count int64
	if err := db.Model(&store.Job{}).Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 0 {
		t.Errorf("job created despite missing CSRF: %d", count)
	}
}

// TestPresetListRedirectsAnonymous 覆盖匿名访问重定向到登录页。
func TestPresetListRedirectsAnonymous(t *testing.T) {
	srv, _, _, _, _, _ := newPresetsServer(t)
	rec := getWithCookies(t, srv, "/presets", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /presets (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /presets (anonymous) Location = %q, want /login", loc)
	}
}

// TestPresetListShowsDefaultWeights 证明未优化的预设显示「默认权重」，且不显示权重明细。
func TestPresetListShowsDefaultWeights(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	seedPreset(t, db, ownerID)
	page := getWithCookies(t, srv, "/presets", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /presets status = %d, want 200 (body %s)", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), "默认权重") {
		t.Errorf("GET /presets does not show the default weight source: %s", page.Body.String())
	}
}

// TestPresetCardFragmentRendersOnlyTheCard 断言卡片片段不含整页外壳（它是 htmx 的局部片段）。
func TestPresetCardFragmentRendersOnlyTheCard(t *testing.T) {
	fragment := renderPresetCardFragment(t)
	if strings.Contains(fragment, "<html") {
		t.Errorf("card fragment contains the full page shell: %s", fragment)
	}
	if !strings.Contains(fragment, `id="preset-7"`) {
		t.Errorf("card fragment is missing its stable id: %s", fragment)
	}
}

// renderPresetCardFragment 直接渲染卡片组件，检查它是局部片段而非整页。
func renderPresetCardFragment(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	card := views.PresetCardData{ID: "7", Name: "fragment"}
	if err := views.PresetCard(card).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render card: %v", err)
	}
	return sb.String()
}

// ---- M3-14 预设管理：新建、编辑与默认预设 ------------------------------------

// TestPresetFreshAccountGetsDefaultPreset 是 M3-14 的主验收（上半）：全新账号首次访问
// 卡组表单或预设页时自动获得一个默认预设，下拉因此非空；重复访问不会重复创建。
func TestPresetFreshAccountGetsDefaultPreset(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil

	// 卡组表单：全新账号的下拉必须已有选项。
	decks := getWithCookies(t, srv, "/decks", cookies)
	if decks.Code != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", decks.Code, snippet(decks.Body.String()))
	}
	if !strings.Contains(decks.Body.String(), `name="preset_id"`) {
		t.Fatalf("deck form has no preset dropdown: %s", snippet(decks.Body.String()))
	}
	// 默认预设名是稳定字面量（store.DefaultPresetName），不再随语言变化（M3-14）。
	if !strings.Contains(decks.Body.String(), store.DefaultPresetName) {
		t.Errorf("fresh account's deck form has an empty preset dropdown: %s", snippet(decks.Body.String()))
	}

	// 预设页：同样能看到这个默认预设。
	presets := getWithCookies(t, srv, "/presets", cookies)
	if presets.Code != http.StatusOK {
		t.Fatalf("GET /presets status = %d, want 200 (body %s)", presets.Code, snippet(presets.Body.String()))
	}
	if !strings.Contains(presets.Body.String(), ">"+store.DefaultPresetName+"</h2>") {
		t.Errorf("fresh account's preset page does not show the default preset: %s", snippet(presets.Body.String()))
	}

	// 幂等：两次访问后仍只有一个预设。
	var n int64
	if err := db.Model(&store.Preset{}).Where("owner_user_id = ?", ownerID).Count(&n).Error; err != nil {
		t.Fatalf("count presets: %v", err)
	}
	if n != 1 {
		t.Errorf("presets for a fresh account = %d after two page loads, want 1", n)
	}
}

// TestPresetCreateAddsSecondPresetShownInDeckForm 覆盖「用户创建第二个预设并能在卡组表单里选到」。
func TestPresetCreateAddsSecondPresetShownInDeckForm(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	// GET /decks 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退列表页（DESIGN.md §8.5）。
	srv.spa = nil
	seedPreset(t, db, ownerID)

	rec := postForm(t, srv, "/presets", url.Values{
		"csrf_token":            {csrf},
		"name":                  {"Evening"},
		"desired_retention":     {"0.85"},
		"learning_steps":        {"2m,20m"},
		"relearning_steps":      {"15m"},
		"maximum_interval_days": {"1000"},
		"enable_fuzz":           {"on"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /presets status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); loc != "/presets" {
		t.Errorf("POST /presets Location = %q, want /presets", loc)
	}

	var got store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "Evening").First(&got).Error; err != nil {
		t.Fatalf("second preset was not persisted: %v", err)
	}
	if got.DesiredRetention != 0.85 || got.LearningSteps != "2m,20m" || got.RelearningSteps != "15m" ||
		got.MaximumIntervalDays != 1000 || !got.FuzzEnabled() {
		t.Errorf("created preset = %+v, want 0.85 / 2m,20m / 15m / 1000 / fuzz on", got)
	}

	decks := getWithCookies(t, srv, "/decks", cookies)
	if !strings.Contains(decks.Body.String(), "Evening") {
		t.Errorf("deck form does not offer the new preset: %s", snippet(decks.Body.String()))
	}
}

// TestPresetCreateStoresFuzzOff 证明未勾选的 fuzz 复选框落库为 false（AGENTS.md §2.3 第 9 条）。
func TestPresetCreateStoresFuzzOff(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	seedPreset(t, db, ownerID)

	rec := postForm(t, srv, "/presets", url.Values{
		"csrf_token":            {csrf},
		"name":                  {"No fuzz"},
		"desired_retention":     {"0.9"},
		"learning_steps":        {""},
		"relearning_steps":      {""},
		"maximum_interval_days": {"365"},
		// 不提交 enable_fuzz → 关闭
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /presets status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "No fuzz").First(&got).Error; err != nil {
		t.Fatalf("preset was not persisted: %v", err)
	}
	if got.FuzzEnabled() {
		t.Errorf("preset fuzz = true, want false: an unchecked box must persist as false")
	}
	if got.LearningSteps != "" || got.RelearningSteps != "" {
		t.Errorf("empty steps = (%q, %q), want both empty", got.LearningSteps, got.RelearningSteps)
	}
}

// TestPresetUpdateChangesValuesAndRejectsInvalid 覆盖编辑主验收：合法编辑改值；
// 保留率 0 / 1 / >1 与不可解析、负数步骤被拒（400 + 本地化消息）且不写库。
func TestPresetUpdateChangesValuesAndRejectsInvalid(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	target := "/presets/" + strconv.FormatUint(p.ID, 10)
	ctx := context.Background()

	rec := postForm(t, srv, target, url.Values{
		"csrf_token":            {csrf},
		"name":                  {"Renamed"},
		"desired_retention":     {"0.80"},
		"learning_steps":        {"5m"},
		"relearning_steps":      {""},
		"maximum_interval_days": {"500"},
		"enable_fuzz":           {"on"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /presets/:id status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	got, err := store.NewPresetStore(db).ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("reload preset: %v", err)
	}
	if got.Name != "Renamed" || got.DesiredRetention != 0.8 || got.LearningSteps != "5m" ||
		got.RelearningSteps != "" || got.MaximumIntervalDays != 500 || !got.FuzzEnabled() {
		t.Errorf("updated preset = %+v, want Renamed / 0.8 / 5m / empty / 500 / fuzz on", got)
	}

	cases := []struct {
		name     string
		override url.Values
		wantMsg  string
	}{
		{"retention zero", url.Values{"desired_retention": {"0"}}, "目标保留率"},
		{"retention one", url.Values{"desired_retention": {"1"}}, "目标保留率"},
		{"retention above one", url.Values{"desired_retention": {"1.5"}}, "目标保留率"},
		{"unparsable learning steps", url.Values{"learning_steps": {"abc"}}, "学习步骤"},
		{"negative learning steps", url.Values{"learning_steps": {"-5m"}}, "学习步骤"},
		{"unparsable relearning steps", url.Values{"relearning_steps": {"7x"}}, "学习步骤"},
	}
	for _, tc := range cases {
		form := url.Values{
			"csrf_token": {csrf}, "name": {"Renamed"}, "desired_retention": {"0.80"},
			"learning_steps": {"5m"}, "relearning_steps": {""}, "maximum_interval_days": {"500"},
			"enable_fuzz": {"on"},
		}
		for k, v := range tc.override {
			form[k] = v
		}
		res := postForm(t, srv, target, form, cookies)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", tc.name, res.Code, snippet(res.Body.String()))
			continue
		}
		if !strings.Contains(res.Body.String(), tc.wantMsg) {
			t.Errorf("%s: body lacks the localised message %q: %s", tc.name, tc.wantMsg, snippet(res.Body.String()))
		}
		after, err := store.NewPresetStore(db).ByID(ctx, p.ID)
		if err != nil {
			t.Fatalf("reload preset after %s: %v", tc.name, err)
		}
		if after.Name != "Renamed" || after.DesiredRetention != 0.8 || after.LearningSteps != "5m" ||
			after.MaximumIntervalDays != 500 {
			t.Errorf("%s: preset changed despite rejection: %+v", tc.name, after)
		}
	}
}

// TestPresetCreateRejectsInvalidValues 覆盖新建的校验拒绝：非法值 400 + 本地化消息且不写库。
func TestPresetCreateRejectsInvalidValues(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	seedPreset(t, db, ownerID)

	cases := []struct {
		name     string
		override url.Values
		wantMsg  string
	}{
		{"retention zero", url.Values{"desired_retention": {"0"}}, "目标保留率"},
		{"retention one", url.Values{"desired_retention": {"1"}}, "目标保留率"},
		{"retention above one", url.Values{"desired_retention": {"2"}}, "目标保留率"},
		{"unparsable steps", url.Values{"learning_steps": {"1m,zzz"}}, "学习步骤"},
	}
	for _, tc := range cases {
		form := url.Values{
			"csrf_token": {csrf}, "name": {"Bad"}, "desired_retention": {"0.9"},
			"learning_steps": {"1m"}, "relearning_steps": {"10m"}, "maximum_interval_days": {"100"},
		}
		for k, v := range tc.override {
			form[k] = v
		}
		res := postForm(t, srv, "/presets", form, cookies)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", tc.name, res.Code, snippet(res.Body.String()))
			continue
		}
		if !strings.Contains(res.Body.String(), tc.wantMsg) {
			t.Errorf("%s: body lacks the localised message %q: %s", tc.name, tc.wantMsg, snippet(res.Body.String()))
		}
	}
	var n int64
	if err := db.Model(&store.Preset{}).Where("owner_user_id = ?", ownerID).Count(&n).Error; err != nil {
		t.Fatalf("count presets: %v", err)
	}
	if n != 1 {
		t.Errorf("invalid creates wrote presets: count = %d, want 1", n)
	}
}

// TestPresetUpdateOtherUsersPresetIsNotFound 是必测负例：他人预设不可编辑，返回 404 而非 403，
// 以免通过状态码泄露 id 是否存在。
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

	rec := postForm(t, srv, "/presets/"+strconv.FormatUint(foreign.ID, 10), url.Values{
		"csrf_token": {csrf}, "name": {"hijack"}, "desired_retention": {"0.9"},
		"learning_steps": {"1m"}, "relearning_steps": {"10m"}, "maximum_interval_days": {"100"},
	}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST another user's preset status = %d, want 404 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after, err := store.NewPresetStore(db).ByID(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("reload foreign preset: %v", err)
	}
	if after.Name != "foreign" {
		t.Errorf("foreign preset was modified: name = %q, want unchanged", after.Name)
	}
}

// TestPresetCreateAndUpdateRequireCSRF 是必测负例：两个写入口缺 CSRF token 都被拒且不落库。
func TestPresetCreateAndUpdateRequireCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _, _ := newPresetsServer(t)
	p := seedPreset(t, db, ownerID)
	for _, target := range []string{"/presets", "/presets/" + strconv.FormatUint(p.ID, 10)} {
		rec := postForm(t, srv, target, url.Values{"name": {"no csrf"}}, cookies)
		if rec.Code != http.StatusForbidden {
			t.Errorf("POST %s without CSRF status = %d, want 403 (body %s)", target, rec.Code, snippet(rec.Body.String()))
		}
	}
	var n int64
	if err := db.Model(&store.Preset{}).Count(&n).Error; err != nil {
		t.Fatalf("count presets: %v", err)
	}
	if n != 1 {
		t.Errorf("preset count = %d after CSRF-less writes, want 1", n)
	}
}

// TestPresetSelectedWhenCreatingDeck 覆盖「编辑后的第二个预设被选中用于新建卡组」。
func TestPresetSelectedWhenCreatingDeck(t *testing.T) {
	srv, db, ownerID, cookies, csrf, _ := newPresetsServer(t)
	seedPreset(t, db, ownerID)

	rec := postForm(t, srv, "/presets", url.Values{
		"csrf_token": {csrf}, "name": {"Second"}, "desired_retention": {"0.88"},
		"learning_steps": {"1m,10m"}, "relearning_steps": {"10m"}, "maximum_interval_days": {"1000"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create second preset status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var second store.Preset
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "Second").First(&second).Error; err != nil {
		t.Fatalf("second preset missing: %v", err)
	}

	deckRec := postForm(t, srv, "/decks", url.Values{
		"csrf_token": {csrf}, "name": {"Deck with second"}, "preset_id": {strconv.FormatUint(second.ID, 10)},
	}, cookies)
	if deckRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /decks status = %d, want 303 (body %s)", deckRec.Code, snippet(deckRec.Body.String()))
	}
	var deck store.Deck
	if err := db.Where("owner_user_id = ? AND name = ?", ownerID, "Deck with second").First(&deck).Error; err != nil {
		t.Fatalf("deck missing: %v", err)
	}
	if deck.PresetID != second.ID {
		t.Errorf("deck preset = %d, want the selected second preset %d", deck.PresetID, second.ID)
	}
}
