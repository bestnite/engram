//go:build unix

// Compiles only on Unix: the fake command goes through `/bin/sh -c ...` (same reason as the file header note in jobs_test.go).

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedPresetDeckCards creates a deck attached to presetID and owned by ownerID with n cards in it, returning the card primary keys.
// Optimization only uses reviews on "decks attached to that preset", so the reviews serving as fuel must land on such cards.
func seedPresetDeckCards(t *testing.T, db *gorm.DB, ownerID, presetID uint64, n int) []uint64 {
	t.Helper()
	now := time.Now().UTC()
	deck := store.Deck{OwnerUserID: ownerID, Name: "optimize deck", PresetID: presetID, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		note := store.Note{DeckID: deck.ID, FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&note).Error; err != nil {
			t.Fatalf("create note: %v", err)
		}
		card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
		if err := db.Create(&card).Error; err != nil {
			t.Fatalf("create card: %v", err)
		}
		ids = append(ids, card.ID)
	}
	return ids
}

// seedOptimizeReviews creates a preset owned by userID plus a deck attached to it, writes n review-log entries as fuel for the optimization threshold,
// and returns the preset primary key. Only the count matters, so fields take their minimum legal values.
func seedOptimizeReviews(t *testing.T, db *gorm.DB, userID uint64, n int) uint64 {
	t.Helper()
	p := store.NewPreset(userID, "optimize preset")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	card := seedPresetDeckCards(t, db, userID, p.ID, 1)[0]
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		rv := store.Review{
			CardID:      card,
			UserID:      userID,
			Rating:      3,
			GradeSource: "self",
			ReviewedAt:  at.Add(time.Duration(i) * time.Minute),
			ReviewDay:   "2026-10-02",
			StateBefore: 2,
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("create review %d: %v", i, err)
		}
	}
	return p.ID
}

// okBuilder returns a command that necessarily exits successfully, for cases that need a real job execution.
func okBuilder(context.Context, *store.Job, Reporter) (Command, error) {
	return Command{Name: "/bin/sh", Args: []string{"-c", "exit 0"}}, nil
}

// TestOptimiseThresholdRefusesBelowThreshold is the "threshold" case (one of the acceptance items): a preset with too few reviews is refused,
// and the error names the shortfall (how many more are needed). The default threshold is 500, there are only 3 here, so the shortfall should be 497.
func TestOptimiseThresholdRefusesBelowThreshold(t *testing.T) {
	runner, _, db := newTestRunner(t, time.Second, okBuilder)
	presetID := seedOptimizeReviews(t, db, 1, 3)
	// Reviews on another preset of the same user do not count (the threshold counts per preset).
	seedOptimizeReviews(t, db, 1, store.DefaultOptimizeMinReviews)

	_, err := runner.EnqueueOptimize(context.Background(), 1, presetID)
	if !errors.Is(err, ErrInsufficientReviews) {
		t.Fatalf("EnqueueOptimize() error = %v, want ErrInsufficientReviews", err)
	}
	var te *ThresholdError
	if !errors.As(err, &te) {
		t.Fatalf("EnqueueOptimize() error %v does not carry *ThresholdError", err)
	}
	wantShortfall := int64(store.DefaultOptimizeMinReviews - 3)
	if te.Shortfall != wantShortfall {
		t.Errorf("Shortfall = %d, want %d", te.Shortfall, wantShortfall)
	}
	if te.PresetID != presetID || te.Reviews != 3 || te.Min != store.DefaultOptimizeMinReviews {
		t.Errorf("ThresholdError = %+v, want preset %d / reviews 3 / min %d", te, presetID, store.DefaultOptimizeMinReviews)
	}
	if !strings.Contains(err.Error(), "short by 497") {
		t.Errorf("error %q does not name the shortfall (want %q)", err.Error(), "short by 497")
	}
	t.Logf("below-threshold refusal: %v", err)
}

// TestOptimiseThresholdReadsSettings proves the threshold can be overridden by an administrator through settings, and that the override is constrained
// by a 300-review floor: a stored value of 2 (written straight to the database, bypassing the form) is clamped to 300 by the read path,
// so exactly 300 reviews can be enqueued — if the read side still let 2 through, this case would lose its point because the threshold would be a formality.
func TestOptimiseThresholdReadsSettings(t *testing.T) {
	runner, _, db := newTestRunner(t, time.Second, okBuilder)
	ctx := context.Background()
	presetID := seedOptimizeReviews(t, db, 1, store.MinOptimizeMinReviews)

	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, "2", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	min, err := store.OptimizeMinReviews(ctx, db)
	if err != nil {
		t.Fatalf("OptimizeMinReviews: %v", err)
	}
	if min != store.MinOptimizeMinReviews {
		t.Fatalf("OptimizeMinReviews() with stored 2 = %d, want the floor %d", min, store.MinOptimizeMinReviews)
	}

	job, err := runner.EnqueueOptimize(ctx, 1, presetID)
	if err != nil {
		t.Fatalf("EnqueueOptimize() with the floor threshold: %v", err)
	}
	if job.TargetID == nil || *job.TargetID != presetID {
		t.Errorf("job.TargetID = %v, want %d", job.TargetID, presetID)
	}
}

// TestOptimiseResultStoresFitMetricsOnJobRow is another acceptance item: the fit metrics are stored on the job row.
// With no adapter present it goes through the FinishOptimize placeholder path, asserting that before/after in result_json can be parsed.
func TestOptimiseResultStoresFitMetricsOnJobRow(t *testing.T) {
	runner, st, _ := newTestRunner(t, time.Second, okBuilder)
	ctx := context.Background()

	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	result := store.OptimizeResult{
		ReviewsUsed: 500,
		Weights:     []float64{0.4, 0.6, 2.4},
		FitBefore:   store.FitMetrics{LogLoss: 0.5412, RMSE: 0.6011},
		FitAfter:    store.FitMetrics{LogLoss: 0.5103, RMSE: 0.5877},
		OptimizedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	if err := runner.FinishOptimize(ctx, job.ID, result, "adapter ok"); err != nil {
		t.Fatalf("FinishOptimize: %v", err)
	}

	got, err := st.ByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, StatusSucceeded)
	}
	if got.ResultJSON == nil {
		t.Fatal("ResultJSON is nil, want the fit report to be stored on the job row")
	}
	var decoded store.OptimizeResult
	if err := json.Unmarshal([]byte(*got.ResultJSON), &decoded); err != nil {
		t.Fatalf("decode result_json %q: %v", *got.ResultJSON, err)
	}
	if decoded.FitBefore.LogLoss != result.FitBefore.LogLoss || decoded.FitAfter.LogLoss != result.FitAfter.LogLoss {
		t.Errorf("log loss before/after = %v/%v, want %v/%v",
			decoded.FitBefore.LogLoss, decoded.FitAfter.LogLoss, result.FitBefore.LogLoss, result.FitAfter.LogLoss)
	}
	if decoded.FitBefore.RMSE != result.FitBefore.RMSE || decoded.FitAfter.RMSE != result.FitAfter.RMSE {
		t.Errorf("rmse before/after = %v/%v, want %v/%v",
			decoded.FitBefore.RMSE, decoded.FitAfter.RMSE, result.FitBefore.RMSE, result.FitAfter.RMSE)
	}
	if decoded.ReviewsUsed != 500 || len(decoded.Weights) != 3 {
		t.Errorf("result_json = %+v, want reviews_used 500 and 3 weights", decoded)
	}
	if !decoded.Improved() {
		t.Errorf("Improved() = false, want true (after log loss %v < before %v)",
			decoded.FitAfter.LogLoss, decoded.FitBefore.LogLoss)
	}
	t.Logf("job %d result_json = %s", got.ID, *got.ResultJSON)
}
