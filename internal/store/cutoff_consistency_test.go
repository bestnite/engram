package store_test

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewDayCutoffNormalizationConsistent 是“口径不一致”的回归用例。
//
// store.ReviewDayString 是库内复习日（review_day）口径，schedule.ReviewDay 是调度侧
// 复习日口径：对同一个 cutoffHour，两边必须算出同一天。0 是午夜，
// 越界值才回退到默认 4；未配置的 nil 在调用计算之前由 ResolveCutoff 解析。
func TestReviewDayCutoffNormalizationConsistent(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	// 本地 00:30 能区分午夜与默认 4；越界输入则必须与默认值一致。
	now := time.Date(2026, 10, 2, 0, 30, 0, 0, loc)
	defaultDay := schedule.ReviewDay(now, loc, schedule.DefaultDayCutoffHour)

	cases := []struct {
		name   string
		cutoff int
		want   string
	}{
		{"zero means midnight", 0, "2026-10-02"},
		{"over range -> default", 25, defaultDay},
		{"negative -> default", -1, defaultDay},
		{"in-range cutoff is kept", 13, schedule.ReviewDay(now, loc, 13)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStore := store.ReviewDayString(now, loc, tc.cutoff)
			gotSched := schedule.ReviewDay(now, loc, tc.cutoff)
			if gotStore != tc.want {
				t.Errorf("store.ReviewDayString(cutoff=%d) = %q, want %q", tc.cutoff, gotStore, tc.want)
			}
			if gotSched != tc.want {
				t.Errorf("schedule.ReviewDay(cutoff=%d) = %q, want %q", tc.cutoff, gotSched, tc.want)
			}
			if gotStore != gotSched {
				t.Errorf("review-day start disagrees for cutoff=%d: store=%q schedule=%q",
					tc.cutoff, gotStore, gotSched)
			}
		})
	}
}

// TestLoadLocationFallback 钉住唯一时区解析入口 store.LoadLocation 的契约：空串与非法 IANA 名
// 都回退 UTC，合法名原样解析。C2 把五份时区解析收敛成这一份后，所有调用方（web/api/reminder/
// digest/schedule）共同依赖这个回退行为，因此这里必须有用例守住它。
func TestLoadLocationFallback(t *testing.T) {
	if got := store.LoadLocation(""); got != time.UTC {
		t.Errorf("LoadLocation(\"\") = %v, want UTC", got)
	}
	if got := store.LoadLocation("Not/AZone"); got != time.UTC {
		t.Errorf("LoadLocation(\"Not/AZone\") = %v, want UTC", got)
	}
	if got := store.LoadLocation("Asia/Shanghai"); got.String() != "Asia/Shanghai" {
		t.Errorf("LoadLocation(\"Asia/Shanghai\") = %v, want Asia/Shanghai", got)
	}
}
