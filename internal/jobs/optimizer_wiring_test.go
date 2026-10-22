//go:build unix

// Compiles only on Unix: it depends on the unix-only test scaffolding in jobs_test.go (newTestRunner and others),
// or uses `/bin/sh` directly as the fake command.

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// newWiringDB opens a temporary SQLite database and migrates it, for the wiring cases to reuse.
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

// newWiringRunner builds and starts a Runner with the real Optimizer.
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

// seedOptimizerUserAndPreset creates a user and a preset belonging to them, returning both.
func seedOptimizerUserAndPreset(t *testing.T, db *gorm.DB) (*store.User, *store.Preset) {
	t.Helper()
	ctx := context.Background()
	u := &store.User{
		Username: "opt-user", Email: "opt-user@example.com", DisplayName: "Opt User",
		Role: "user", Status: "active", Locale: "en", Timezone: "UTC", DayCutoffHour: store.Ptr(0),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(db).Create(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}
	p := &store.Preset{
		OwnerUserID: u.ID, Name: "Default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
	}
	if err := store.NewPresetStore(db).Create(ctx, p); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	return u, p
}

// seedAdapterReviews writes review logs spanning many days and many cards, giving the adapter enough "long-term review" items to train on
// (the adapter requires at least 8 items after filtering). Same-card entries step by day, delta_t > 0, and ratings vary between 1..4.
func seedAdapterReviews(t *testing.T, db *gorm.DB, userID, presetID uint64, cards, days int) {
	t.Helper()
	cardIDs := seedPresetDeckCards(t, db, userID, presetID, cards)
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for card := 0; card < cards; card++ {
		for day := 0; day < days; day++ {
			at := base.AddDate(0, 0, day).Add(time.Duration(card) * time.Minute)
			rv := store.Review{
				CardID:      cardIDs[card],
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

// TestOptimizeEndToEndRunsRealAdapter is the end-to-end acceptance: it drives a preset with enough reviews to clear the threshold
// through the runner and the real Rust adapter, asserting the job succeeds and both the job row and the preset carry 21-dimension weights.
// It skips when the adapter is not built (CI has no Rust toolchain); it is built on this machine, so this really runs.
func TestOptimizeEndToEndRunsRealAdapter(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s; run `cargo build --release` in tools/optimizer", bin)
	}
	ctx := context.Background()
	db, st := newWiringDB(t)
	u, p := seedOptimizerUserAndPreset(t, db)
	// 8 cards × 40 days = 320 reviews: clears the 300-review threshold floor, and is enough for the adapter to train.
	const cards, days = 8, 40
	seedAdapterReviews(t, db, u.ID, p.ID, cards, days)
	// The threshold is set to its floor: the default 500 would block these 320 reviews.
	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, strconv.Itoa(store.MinOptimizeMinReviews), nil, time.Now().UTC()); err != nil {
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

// TestOptimizeJobFailsWhenAdapterMissing is the second acceptance item: when the adapter is missing the job must
// fail, and the failure message must name the expected path, so operators see at a glance which file is missing.
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

// TestResolveOptimizerBinary covers path resolution: an explicit configuration wins; otherwise look next to the service binary first,
// and fall back to the repository build artifact when that is not found.
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

	// In an empty directory with neither a side-by-side binary nor a repository artifact relative to the current directory, it should fall back to the repository build path.
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
