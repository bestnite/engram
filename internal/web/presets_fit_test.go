package web

import (
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestOptimizeVerdictThreeState 是验收 3：优化前后拟合对比的三态判据
// （原先由 SSR 卡片渲染的 applyResultToCard 覆盖，SSR 页面层删除后该判据的唯一实现是
// optimizeVerdict，测试随之盯住它）。
//
//   - 没有任何可用指标 -> unavailable（前端不渲染结论，避免把零值误报成「未改善」）；
//   - 两个指标都算出来但样本不足（可用 item 数 < MinFitItems）-> insufficient_sample；
//   - 样本足够且 after < before -> improved；after > before -> not_improved。
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
