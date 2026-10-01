package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// retroWindow 覆盖 fixtures 里全部复习日的闭区间（fixture 的日志都在 2026 年内）。
const (
	retroFrom  = "2026-01-01"
	retroTo    = "2026-12-31"
	retroToday = "2026-10-02"
)

// TestRetroCheckRecomputesPageNumbers 是 M7-4 的正面验收：统计页数字与从原始表
// 重算的结果必须逐项一致，否则报出漂移项。
func TestRetroCheckRecomputesPageNumbers(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()
			drift, err := RetroCheck(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("RetroCheck() error = %v", err)
			}
			if len(drift) != 0 {
				t.Fatalf("RetroCheck() found drift: %v", drift)
			}
			page, err := PageNumbersFromStats(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("PageNumbersFromStats() error = %v", err)
			}
			// 数值不为零，证明校验真的跑在数据上而不是空集通过。
			if page.ReviewToday == 0 || page.DueToday == 0 || page.RetentionTotal == 0 || page.CurveNew+page.CurveReview == 0 {
				t.Fatalf("fixture produced all-zero page numbers %+v; the check would pass vacuously", page)
			}
			t.Logf("page numbers = %+v", page)
		})
	}
}

// TestRetroCheckCatchesCorruptedAggregate 是 M7-4 的反面验收：故意破坏一个聚合值，
// 校验必须失败并指名该数字。这里破坏的是页面侧的 review_volume.today——正是「数字漂移」
// 的典型形态（聚合代码改坏，而原始表没变）。
func TestRetroCheckCatchesCorruptedAggregate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()

			page, err := PageNumbersFromStats(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("PageNumbersFromStats() error = %v", err)
			}
			raw, err := RecomputePageNumbers(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("RecomputePageNumbers() error = %v", err)
			}
			if drift := DiffPageNumbers(page, raw); len(drift) != 0 {
				t.Fatalf("baseline should be clean, got drift %v", drift)
			}

			corrupted := page
			corrupted.ReviewToday++ // 把一个聚合值改坏
			drift := DiffPageNumbers(corrupted, raw)
			if len(drift) != 1 {
				t.Fatalf("corrupted aggregate produced %d drift(s) %v, want exactly 1", len(drift), drift)
			}
			if !strings.Contains(drift[0], "review_volume.today") {
				t.Fatalf("drift %q does not name the corrupted aggregate review_volume.today", drift[0])
			}
			t.Logf("corrupted aggregate detected: %s", drift[0])

			// 再破坏一个不同条目的聚合值，校验同样要抓到。
			corrupted = page
			corrupted.DueNewNotDue += 99
			drift = DiffPageNumbers(corrupted, raw)
			if len(drift) != 1 || !strings.Contains(drift[0], "due_forecast.new_not_due") {
				t.Fatalf("corrupted DueNewNotDue -> drift %v, want due_forecast.new_not_due", drift)
			}
			t.Logf("second corrupted aggregate detected: %s", drift[0])
		})
	}
}
