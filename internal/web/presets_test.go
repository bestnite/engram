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

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/jobs"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
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
	accounts, err := auth.NewAccountService(users, sessions, auth.NewPasswordHasher(auth.Params{
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

// seedPreset 建一个属于 owner 的预设。
func seedPreset(t *testing.T, db *gorm.DB, ownerID uint64) *store.Preset {
	t.Helper()
	p := store.NewPreset(ownerID, "Test preset")
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
	setMinReviews(t, db, 1)
	seedReviews(t, db, ownerID, 1)

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
	setMinReviews(t, db, 1)
	seedReviews(t, db, ownerID, 1)

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
		FitBefore:   store.FitMetrics{LogLoss: 0.51, RMSE: 0.42},
		FitAfter:    store.FitMetrics{LogLoss: 0.44, RMSE: 0.38},
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
