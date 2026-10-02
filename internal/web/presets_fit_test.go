package web

import (
	"encoding/json"
	"testing"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// succeededJobWithResult 造一个 succeeded 的作业行，result_json 为给定结果。
func succeededJobWithResult(t *testing.T, result store.OptimizeResult) *store.Job {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	encoded := string(raw)
	return &store.Job{Status: jobs.StatusSucceeded, ResultJSON: &encoded}
}

// TestPresetFitVerdictHiddenWhenNoItems 是 M9-11 验收 5：没有可预测 item（指标全为零值）时
// 页面不得声称「未改善」——ResultBefore/After/Verdict 保持空串，模板守卫因此不渲染结论区。
func TestPresetFitVerdictHiddenWhenNoItems(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(language.English)
	srv := &Server{logger: discardLogger()}

	job := succeededJobWithResult(t, store.OptimizeResult{ReviewsUsed: 12})
	var card views.PresetCardData
	srv.applyResultToCard(loc, &card, job)

	if card.ResultBefore != "" || card.ResultAfter != "" || card.ResultVerdict != "" {
		t.Fatalf("page claims a verdict without any predictable item: before=%q after=%q verdict=%q",
			card.ResultBefore, card.ResultAfter, card.ResultVerdict)
	}
	if card.ResultVerdict == loc.T("presets.optimize.not_improved") {
		t.Fatalf("verdict equals the \"not improved\" text even though no metric was available")
	}
	// 第三种状态（样本不足）也不能被误触发：没有任何指标时连「样本不足」都不该出现。
	if card.ResultSampleInsufficient != "" {
		t.Fatalf("sample-insufficient text rendered without any metric: %q", card.ResultSampleInsufficient)
	}
	t.Logf("no-item job -> before=%q after=%q verdict=%q insufficient=%q (section hidden)",
		card.ResultBefore, card.ResultAfter, card.ResultVerdict, card.ResultSampleInsufficient)
}

// TestPresetFitVerdictSampleInsufficient 是 M9-12 验收 3 的反面：两个指标都算出来了
// （Available() 为真），但可用 item 数低于 MinFitItems 时，页面只渲染「样本不足，无法判定」，
// 既不渲染 before/after 两行，也不渲染「改善/未改善」结论——一次本就没机会的优化
// 不该被误报成「未改善」。
func TestPresetFitVerdictSampleInsufficient(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(language.English)
	srv := &Server{logger: discardLogger()}

	small := store.MinFitItems - 1
	job := succeededJobWithResult(t, store.OptimizeResult{
		ReviewsUsed: 210,
		FitBefore:   store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: small},
		FitAfter:    store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: small},
	})
	var card views.PresetCardData
	srv.applyResultToCard(loc, &card, job)

	if card.ResultSampleInsufficient != loc.T("presets.optimize.sample_insufficient") {
		t.Fatalf("sample-insufficient text = %q, want %q", card.ResultSampleInsufficient, loc.T("presets.optimize.sample_insufficient"))
	}
	if card.ResultBefore != "" || card.ResultAfter != "" {
		t.Errorf("before/after must stay hidden when the sample is too small: before=%q after=%q", card.ResultBefore, card.ResultAfter)
	}
	if card.ResultVerdict != "" {
		t.Errorf("verdict = %q, want empty (a too-small sample must not be judged)", card.ResultVerdict)
	}
	t.Logf("small-sample job (%d items, both metrics non-zero) -> insufficient=%q before=%q after=%q verdict=%q",
		small, card.ResultSampleInsufficient, card.ResultBefore, card.ResultAfter, card.ResultVerdict)
}

// TestPresetFitVerdictRenderedWhenMetricsAvailable 是 M9-12 验收 3 的正面：两个指标都算出来
// 且 item 数达到 MinFitItems 时，页面照常给出结论——after < before 显示「改善」，
// after > before 显示「未改善」。
func TestPresetFitVerdictRenderedWhenMetricsAvailable(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(language.English)
	srv := &Server{logger: discardLogger()}

	const items = store.MinFitItems + 50
	improved := succeededJobWithResult(t, store.OptimizeResult{
		ReviewsUsed: 260,
		FitBefore:   store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: items},
		FitAfter:    store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: items},
	})
	var card views.PresetCardData
	srv.applyResultToCard(loc, &card, improved)
	if card.ResultBefore == "" || card.ResultAfter == "" {
		t.Fatalf("metrics available but not rendered: before=%q after=%q", card.ResultBefore, card.ResultAfter)
	}
	if card.ResultSampleInsufficient != "" {
		t.Errorf("sample-insufficient text rendered despite %d items: %q", items, card.ResultSampleInsufficient)
	}
	if card.ResultVerdict != loc.T("presets.optimize.improved") {
		t.Fatalf("verdict = %q, want improved", card.ResultVerdict)
	}
	t.Logf("improved job (%d items) -> before=%q after=%q verdict=%q", items, card.ResultBefore, card.ResultAfter, card.ResultVerdict)

	worse := succeededJobWithResult(t, store.OptimizeResult{
		ReviewsUsed: 260,
		FitBefore:   store.FitMetrics{LogLoss: 0.20, RMSE: 0.18, Items: items},
		FitAfter:    store.FitMetrics{LogLoss: 0.50, RMSE: 0.40, Items: items},
	})
	var card2 views.PresetCardData
	srv.applyResultToCard(loc, &card2, worse)
	if card2.ResultVerdict != loc.T("presets.optimize.not_improved") {
		t.Fatalf("verdict = %q, want not improved", card2.ResultVerdict)
	}
	t.Logf("regressed job -> verdict=%q", card2.ResultVerdict)
}
