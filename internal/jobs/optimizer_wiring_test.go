package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// newWiringDB 打开临时 SQLite 库并迁移，供 M9-10 接线用例复用。
func newWiringDB(t *testing.T) (*gorm.DB, *Store) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "wiring.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db, NewStore(db)
}

// newWiringRunner 用真实的 Optimizer 构造并启动 Runner。
func newWiringRunner(t *testing.T, db *gorm.DB, st *Store, opt *Optimizer, timeout time.Duration) *Runner {
	t.Helper()
	runner, err := New(Deps{
		DB:       db,
		Store:    st,
		Timeout:  timeout,
		Command:  opt.CommandBuilder(),
		Complete: opt.Complete,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)
	return runner
}

// seedOptimizerUserAndPreset 建一个用户与一个属于他的预设，返回两者。
func seedOptimizerUserAndPreset(t *testing.T, db *gorm.DB) (*store.User, *store.Preset) {
	t.Helper()
	ctx := context.Background()
	u := &store.User{
		Username: "opt-user", Email: "opt-user@example.com", DisplayName: "Opt User",
		Role: "user", Status: "active", Locale: "en", Timezone: "UTC", DayCutoffHour: 0,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(db).Create(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	p := &store.Preset{
		OwnerUserID: u.ID, Name: "Default", DesiredRetention: 0.9,
		LearningSteps: "1m 10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
	}
	if err := store.NewPresetStore(db).Create(ctx, p); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return u, p
}

// seedAdapterReviews 写入跨多天、多张卡的复习日志，让适配器有足够「长期复习」项训练
// （适配器要求过滤后至少 8 项）。同卡按天递增，delta_t > 0，评分在 1..4 间变化。
func seedAdapterReviews(t *testing.T, db *gorm.DB, userID uint64, cards, days int) {
	t.Helper()
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for card := 0; card < cards; card++ {
		for day := 0; day < days; day++ {
			at := base.AddDate(0, 0, day).Add(time.Duration(card) * time.Minute)
			rv := store.Review{
				CardID:      uint64(card + 1),
				UserID:      userID,
				Rating:      1 + (card*3+day)%4,
				GradeSource: "self",
				ReviewedAt:  at,
				ReviewDay:   at.Format("2006-01-02"),
				StateBefore: 2,
			}
			if err := db.Create(&rv).Error; err != nil {
				t.Fatalf("create review (card %d day %d): %v", card, day, err)
			}
		}
	}
}

// TestOptimizeEndToEndRunsRealAdapter 是 M9-10 的端到端验收：驱动一个复习条数足够阈值的
// 预设走完 runner + 真实 Rust 适配器，断言作业 succeeded，且 job 行与 preset 上都有 21 维权重。
// 适配器未构建时跳过（CI 无 Rust 工具链）；本机已构建，因此这里会真跑。
func TestOptimizeEndToEndRunsRealAdapter(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s; run `cargo build --release` in tools/optimizer", bin)
	}
	ctx := context.Background()
	db, st := newWiringDB(t)
	u, p := seedOptimizerUserAndPreset(t, db)
	const cards, days = 4, 10
	seedAdapterReviews(t, db, u.ID, cards, days)
	// 门槛降到 2，让「够训练」的条数不被默认 500 挡住。
	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, "2", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	opt, err := NewOptimizer(OptimizerDeps{DB: db, Binary: bin})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	runner := newWiringRunner(t, db, st, opt, 2*time.Minute)

	job, err := runner.EnqueueOptimize(ctx, u.ID, p.ID)
	if err != nil {
		t.Fatalf("EnqueueOptimize: %v", err)
	}
	done := waitForStatus(t, st, job.ID, StatusSucceeded, 2*time.Minute)
	if done.ResultJSON == nil {
		t.Fatalf("job %d succeeded without result_json", done.ID)
	}

	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*done.ResultJSON), &result); err != nil {
		t.Fatalf("decode result_json %q: %v", *done.ResultJSON, err)
	}
	if len(result.Weights) != 21 {
		t.Fatalf("job weights = %d, want 21 (%v)", len(result.Weights), result.Weights)
	}
	wantReviews := int64(cards * days)
	if result.ReviewsUsed != wantReviews {
		t.Errorf("ReviewsUsed = %d, want %d", result.ReviewsUsed, wantReviews)
	}

	preset, err := store.NewPresetStore(db).ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("reload preset: %v", err)
	}
	if preset.WeightsJSON == nil {
		t.Fatalf("preset %d has no weights_json after a succeeded optimize job", p.ID)
	}
	var presetWeights []float64
	if err := json.Unmarshal([]byte(*preset.WeightsJSON), &presetWeights); err != nil {
		t.Fatalf("decode preset weights_json %q: %v", *preset.WeightsJSON, err)
	}
	if len(presetWeights) != 21 {
		t.Fatalf("preset weights = %d, want 21", len(presetWeights))
	}
	if preset.WeightsOptimizedAt == nil {
		t.Errorf("preset WeightsOptimizedAt is nil")
	}
	if preset.WeightsReviewCount == nil || int64(*preset.WeightsReviewCount) != wantReviews {
		t.Errorf("preset WeightsReviewCount = %v, want %d", preset.WeightsReviewCount, wantReviews)
	}
	t.Logf("job %d succeeded: reviews_used=%d weights=%d tail=%q", done.ID, result.ReviewsUsed, len(result.Weights), deref(done.LogTail))
	t.Logf("preset %d weights_json=%s optimized_at=%v review_count=%v", preset.ID, *preset.WeightsJSON, preset.WeightsOptimizedAt, preset.WeightsReviewCount)
}

// TestOptimizeJobFailsWhenAdapterMissing 是 M9-10 的第二条验收：适配器缺失时作业必须
// 失败，且失败消息点名期望路径，让运维一眼知道缺的是哪个文件。
func TestOptimizeJobFailsWhenAdapterMissing(t *testing.T) {
	ctx := context.Background()
	db, st := newWiringDB(t)
	missing := filepath.Join(t.TempDir(), "no-such-optimizer")

	opt, err := NewOptimizer(OptimizerDeps{DB: db, Binary: missing})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	runner := newWiringRunner(t, db, st, opt, 5*time.Second)

	target := uint64(1)
	job, err := runner.Enqueue(ctx, KindOptimize, &target)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	failed := waitForStatus(t, st, job.ID, StatusFailed, 5*time.Second)
	if failed.Error == nil {
		t.Fatalf("job %d failed without an error message", failed.ID)
	}
	if !strings.Contains(*failed.Error, ErrOptimizerNotFound.Error()) {
		t.Errorf("error %q does not mention %q", *failed.Error, ErrOptimizerNotFound.Error())
	}
	if !strings.Contains(*failed.Error, missing) {
		t.Errorf("error %q does not name the missing adapter path %q", *failed.Error, missing)
	}
	if failed.FinishedAt == nil {
		t.Errorf("failed job missing finished_at")
	}
	t.Logf("job %d status=%s error=%q", failed.ID, failed.Status, *failed.Error)
}

// TestResolveOptimizerBinary 覆盖路径解析：显式配置优先；否则先找服务二进制旁，
// 找不到再回退到仓库构建产物。
func TestResolveOptimizerBinary(t *testing.T) {
	dir := t.TempDir()
	beside := filepath.Join(dir, optimizerBinaryName)
	if err := os.WriteFile(beside, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if got := ResolveOptimizerBinary("", filepath.Join(dir, "engram")); got != beside {
		t.Errorf("beside-binary resolution = %q, want %q", got, beside)
	}
	const custom = "/opt/engram/optimizer-custom"
	if got := ResolveOptimizerBinary(custom, filepath.Join(dir, "engram")); got != custom {
		t.Errorf("configured path = %q, want %q (must be honoured as-is)", got, custom)
	}

	// 空目录里既没有旁路二进制，也没有仓库产物相对当前目录时，应回退到仓库构建路径。
	empty := t.TempDir()
	got := ResolveOptimizerBinary("", filepath.Join(empty, "engram"))
	if got == filepath.Join(empty, optimizerBinaryName) {
		t.Logf("no fallback adapter present on this host; resolver returned %q", got)
		return
	}
	if !fileExists(got) {
		t.Errorf("fallback %q does not exist", got)
	}
	t.Logf("repo fallback resolution = %q", got)
}
