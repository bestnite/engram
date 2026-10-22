package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// This file is the store-layer part of parameter optimization:
// the server-side threshold (reject outright when there are too few reviews, reporting the shortfall) and the data shape of the fit report.
//
// Division of labour: the weight-fitting algorithm belongs to the Rust adapter and never enters Go code; this file only defines
// "how the threshold is decided" and "where the metrics live and what they are called", giving the web layer and the adapter a stable contract.

const (
	// DefaultOptimizeMinReviews is the default optimization threshold (by default fewer than 500 reviews is rejected).
	// Administrators can adjust it through SettingKeyOptimizeMinReviews in the settings table.
	DefaultOptimizeMinReviews = 500

	// SettingKeyOptimizeMinReviews is the settings key for the threshold override. Per the settings table convention the value is stored as JSON-encoded
	// text (see PutSetting / LoadSettings) and holds a decimal integer review count.
	SettingKeyOptimizeMinReviews = "optimize.min_reviews"

	// MinOptimizeMinReviews is the floor of the optimization threshold.
	// Rationale: in a measurement over 250 cards / 1969 real log entries, with fewer than roughly 184 usable items the adapter cannot learn or falls back to
	// the default weights, the metrics before and after optimization are identical, and the page reports "not improved" for an optimization that never had a chance. 184 items
	// is about 210 reviews, so 300 leaves headroom. A threshold below the floor blocks no optimization that would yield a misleading conclusion.
	MinOptimizeMinReviews = 300
)

// OptimizeMinReviews reads the current optimization threshold. When unset, empty or invalid it falls back to the default:
// a typo by an administrator (writing "many", say) must never make the optimize entry point permanently unusable; falling back to the default is explicable behaviour.
func OptimizeMinReviews(ctx context.Context, db *gorm.DB) (int, error) {
	settings, err := LoadSettings(ctx, db)
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(settings[SettingKeyOptimizeMinReviews])
	if raw == "" {
		return DefaultOptimizeMinReviews, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		// A non-positive threshold is meaningless (>=1 is needed to block anything); fall back to the default and let the caller proceed as usual.
		return DefaultOptimizeMinReviews, nil
	}
	if n < MinOptimizeMinReviews {
		// The read path clamps to the floor as well: the settings row may come from a direct database write, an old version or a write that bypassed the form,
		// so form validation cannot be assumed to have run. Note this differs from the default fallback above -- here the value is legal but too low,
		// so it is clamped to the floor instead of being reset to the default.
		return MinOptimizeMinReviews, nil
	}
	return n, nil
}

// CountForPreset returns the number of usable reviews for optimizing presetID (definition in presetReviewsQuery); it is the threshold's input.
func (s *ReviewStore) CountForPreset(ctx context.Context, userID, presetID uint64) (int64, error) {
	var n int64
	if err := presetReviewsQuery(ctx, s.db, userID, presetID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// OptimizeGate is the eligibility verdict for a preset against the current threshold.
// Shortfall only means something when Eligible is false; it is "how many reviews are still missing".
type OptimizeGate struct {
	Reviews    int64
	MinReviews int
	Shortfall  int64
	Eligible   bool
}

// GateOptimize decides whether presetID of userID may run an optimization: read the threshold, count that preset's usable reviews, compute the shortfall.
func GateOptimize(ctx context.Context, db *gorm.DB, userID, presetID uint64) (OptimizeGate, error) {
	if userID == 0 || presetID == 0 {
		return OptimizeGate{}, gorm.ErrPrimaryKeyRequired
	}
	min, err := OptimizeMinReviews(ctx, db)
	if err != nil {
		return OptimizeGate{}, err
	}
	reviews, err := NewReviewStore(db).CountForPreset(ctx, userID, presetID)
	if err != nil {
		return OptimizeGate{}, err
	}
	gate := OptimizeGate{Reviews: reviews, MinReviews: min}
	if reviews < int64(min) {
		gate.Shortfall = int64(min) - reviews
	} else {
		gate.Eligible = true
	}
	return gate, nil
}

// FitMetrics is the fit metric measured once before and once after optimization (the settings page's "fit comparison before/after optimization",
// the idea behind "Check health" in the Anki manual: infer from historical reviews how well the parameters fit the actual outcomes).
//
// Metric definitions (computed by internal/schedule replaying the review log with go-fsrs):
//   - LogLoss: the predicted log loss of each due review; predicted is the probability the parameters give to "this review will be recalled"
//     (0-1), observed is 1 (not Again) or 0 (Again). Smaller is a better fit;
//     this is a scalar on the same scale as the magnitude the Anki optimizer outputs, directly comparable across parameter sets.
//   - RMSE: the root mean square error between the predicted recall probability and the actual outcome (0/1), an intuitive companion to LogLoss,
//     gentler than LogLoss on extreme confidence (predicting 0/1 and being wrong), which suits page display.
//
// Both are computed on a fixed review set, before with the old weights and after with the new ones; only the two scalars on the same
// data set are compared, so "the data set changed" is not mistaken for an improvement in fit.
type FitMetrics struct {
	LogLoss float64 `json:"log_loss"`
	RMSE    float64 `json:"rmse"`
	// Items is the number of predictable items this evaluation actually covered (each card's first review and unpredictable points are excluded).
	// It exists to separate "it computed" from "it computed but the sample is too small":
	// the old result_json lacks this field, so deserializing yields 0, which is backwards compatible.
	Items int `json:"items"`
}

// MinFitItems is the minimum number of items needed to judge "improved/not improved" with confidence.
// Rationale: with fewer than roughly 184 usable items the adapter cannot learn or falls back to the default weights, the metrics before and after optimization are identical,
// and "not improved" misleads about an optimization that never had a chance; 200 leaves headroom, and the verdict has been stable from 184 items in practice.
const MinFitItems = 200

// OptimizeResult is the structure of the optimize job's result_json (jobs.result_json).
// It lives on the job row rather than the preset row: the report belongs to "this one optimization", the preset only stores the weights that finally took effect
// (weights_json / weights_optimized_at / weights_review_count).
type OptimizeResult struct {
	// ReviewsUsed is the number of reviews this training actually used, which helps explain why the threshold was just met.
	ReviewsUsed int64 `json:"reviews_used"`
	// Weights are the 21-dimensional FSRS weights the adapter produced, not yet (or already) written back to the preset.
	Weights []float64 `json:"weights"`
	// FitBefore / FitAfter are the fit metrics computed on the same review set with the old/new weights.
	FitBefore FitMetrics `json:"fit_before"`
	FitAfter  FitMetrics `json:"fit_after"`
	// OptimizedAt is the moment the adapter finished training (UTC).
	OptimizedAt time.Time `json:"optimized_at"`
}

// Improved reports whether the new weights' log loss beats the old ones; equal counts as not improved.
func (r OptimizeResult) Improved() bool { return r.FitAfter.LogLoss < r.FitBefore.LogLoss }

// Available reports whether this fit really covered at least one predictable item.
// With no predictable item at all (e.g. the review log is all first reviews of each card, or nothing can be replayed),
// CompareFit returns the zero value; predicted probabilities are clamped inside the open interval (0,1), so LogLoss is always positive and the zero value means "no metric".
// The preset page uses this to avoid the misleading conclusion "not improved" when there is no metric (acceptance 5).
func (m FitMetrics) Available() bool { return m.LogLoss > 0 }

// SampleSufficient reports whether this fit's sample can support an "improved/not improved" conclusion.
// It differs from Available(): Available() only says "a metric was computed" (at least one item covered),
// this method additionally requires the covered item count to reach MinFitItems. The page renders a conclusion only when both are true;
// when only Available() is true the page renders "sample too small, cannot decide".
func (m FitMetrics) SampleSufficient() bool { return m.Available() && m.Items >= MinFitItems }

// ErrPresetWeightsIDRequired means no preset primary key was given when resetting to the default weights.
var ErrPresetWeightsIDRequired = errors.New("reset preset weights: id is required")

// ResetPresetWeights rolls a preset's optimized weights back to the default with one click (M9 acceptance "can roll back with one click"): weights_json,
// weights_optimized_at and weights_review_count are all written back to NULL together.
//
// Why all three must be cleared together: the scheduler (schedule.NewScheduler) uses whether weights_json is NULL to choose between
// DefaultWeights() and the optimized weights; clearing weights_json alone while leaving weights_optimized_at and
// weights_review_count would make the page show "optimized + time + review count" with no actual weights, which contradicts itself.
// Once all three are cleared at once, the preset reads exactly like a new preset that was never optimized.
//
// Idempotent: calling again on a preset already at the default (all three columns NULL) returns nil too -- "already the default" is not an error,
// the UI button may be clicked repeatedly. Only the owner can roll back; the authorization rule matches Update (presets are not part of the authorization table).
func (s *PresetStore) ResetPresetWeights(ctx context.Context, actorUserID, presetID uint64) error {
	if presetID == 0 {
		return ErrPresetWeightsIDRequired
	}
	existing, err := s.ByID(ctx, presetID)
	if err != nil {
		return err
	}
	if err := requirePresetOwner(existing, actorUserID); err != nil {
		return err
	}
	// nil is written explicitly through a map: GORM does not skip zero values on map updates, so this really sets the column to NULL.
	updates := map[string]any{
		"weights_json":         nil,
		"weights_optimized_at": nil,
		"weights_review_count": nil,
		"updated_at":           time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ?", presetID).Updates(updates).Error; err != nil {
		return fmt.Errorf("reset preset weights: %w", err)
	}
	return nil
}
