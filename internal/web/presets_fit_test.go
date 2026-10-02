package web

import (
	"encoding/json"
	"testing"

	"golang.org/x/text/language"

	"example.com/engram/internal/i18n"
	"example.com/engram/internal/jobs"
	"example.com/engram/internal/store"
	"example.com/engram/internal/web/views"
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
	t.Logf("no-item job -> before=%q after=%q verdict=%q (section hidden)", card.ResultBefore, card.ResultAfter, card.ResultVerdict)
}

// TestPresetFitVerdictRenderedWhenMetricsAvailable 证明有指标时页面照常给出结论：
// after < before 显示「改善」，after > before 显示「未改善」。
func TestPresetFitVerdictRenderedWhenMetricsAvailable(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(language.English)
	srv := &Server{logger: discardLogger()}

	improved := succeededJobWithResult(t, store.OptimizeResult{
		ReviewsUsed: 30,
		FitBefore:   store.FitMetrics{LogLoss: 0.50, RMSE: 0.40},
		FitAfter:    store.FitMetrics{LogLoss: 0.20, RMSE: 0.18},
	})
	var card views.PresetCardData
	srv.applyResultToCard(loc, &card, improved)
	if card.ResultBefore == "" || card.ResultAfter == "" {
		t.Fatalf("metrics available but not rendered: before=%q after=%q", card.ResultBefore, card.ResultAfter)
	}
	if card.ResultVerdict != loc.T("presets.optimize.improved") {
		t.Fatalf("verdict = %q, want improved", card.ResultVerdict)
	}
	t.Logf("improved job -> before=%q after=%q verdict=%q", card.ResultBefore, card.ResultAfter, card.ResultVerdict)

	worse := succeededJobWithResult(t, store.OptimizeResult{
		ReviewsUsed: 30,
		FitBefore:   store.FitMetrics{LogLoss: 0.20, RMSE: 0.18},
		FitAfter:    store.FitMetrics{LogLoss: 0.50, RMSE: 0.40},
	})
	var card2 views.PresetCardData
	srv.applyResultToCard(loc, &card2, worse)
	if card2.ResultVerdict != loc.T("presets.optimize.not_improved") {
		t.Fatalf("verdict = %q, want not improved", card2.ResultVerdict)
	}
	t.Logf("regressed job -> verdict=%q", card2.ResultVerdict)
}
