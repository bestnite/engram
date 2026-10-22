package web

import (
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestOptimizeVerdictThreeState is acceptance 3: the three-state criterion for the before/after fit comparison
// (previously covered by the SSR card rendering's applyResultToCard; with the SSR page layer deleted the only implementation of that criterion is
// optimizeVerdict, so the test watches it instead).
//
//   - no usable metrics at all -> unavailable (the frontend renders no conclusion, so a zero value is not misreported as "not improved");
//   - both metrics computed but the sample insufficient (usable item count < MinFitItems) -> insufficient_sample;
//   - sample sufficient and after < before -> improved; after > before -> not_improved.
func TestOptimizeVerdictThreeState(t *testing.T) {
	items := store.MinFitItems + 50
	cases := []struct {
		name   string
		result store.OptimizeResult
		want   string
	}{
		{
			name:   "no metrics",
			result: store.OptimizeResult{ReviewsUsed: 12},
			want:   "unavailable",
		},
		{
			name: "sample too small",
			result: store.OptimizeResult{
				ReviewsUsed: 210,
				FitBefore:   store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: store.MinFitItems - 1},
				FitAfter:    store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: store.MinFitItems - 1},
			},
			want: "insufficient_sample",
		},
		{
			name: "improved",
			result: store.OptimizeResult{
				ReviewsUsed: 260,
				FitBefore:   store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: items},
				FitAfter:    store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: items},
			},
			want: "improved",
		},
		{
			name: "not improved",
			result: store.OptimizeResult{
				ReviewsUsed: 260,
				FitBefore:   store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: items},
				FitAfter:    store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: items},
			},
			want: "not_improved",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := optimizeVerdict(&tc.result); got != tc.want {
				t.Errorf("optimizeVerdict = %q, want %q", got, tc.want)
			}
		})
	}
}
