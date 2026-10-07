package web

import (
	"testing"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestElapsedLabelUnits 固定"耗时随量级换单位"的口径。边界值既是
// "上提一级"的守卫（59.96 秒 → 1 分钟、59.98 分钟 → 1 小时），也是"亚秒仍写毫秒"
// 的守卫——单次复习的耗时就在这个量级，换成秒会把 420 毫秒说成 0.4 秒。
func TestElapsedLabelUnits(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New() error = %v", err)
	}
	zh := tr.Localizer(tr.Pick("", "", ""))

	cases := []struct {
		ms   int64
		want string
	}{
		{-5, "0 毫秒"},
		{0, "0 毫秒"},
		{420, "420 毫秒"},
		{999, "999 毫秒"},
		{1000, "1 秒"},
		{8400, "8.4 秒"},
		{59_900, "59.9 秒"},
		{59_960, "1 分钟"}, // 59.96 秒 → 60.0 秒，必须上提，不能写 "60 秒"
		{60_000, "1 分钟"},
		{90_000, "1.5 分钟"},
		{1625_000, "27.1 分钟"},
		{3_599_000, "1 小时"}, // 59.98 分钟 → 60.0 分钟，上提到小时
		{3_600_000, "1 小时"},
		{3_660_000, "1 小时 1 分"},
		{9_000_000, "2 小时 30 分"},
	}
	for _, tc := range cases {
		if got := elapsedLabel(zh, tc.ms); got != tc.want {
			t.Errorf("elapsedLabel(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}

	// 英文语言包同样要有单位（两份语言包 key 一致由 i18n 测试把关，这里只确认
	// 模板里的占位符在另一种语言下也渲染得出来）。
	en := tr.Localizer(language.English)
	for _, tc := range []struct {
		ms   int64
		want string
	}{
		{420, "420 ms"},
		{8400, "8.4 s"},
		{90_000, "1.5 min"},
		{9_000_000, "2 h 30 min"},
	} {
		if got := elapsedLabel(en, tc.ms); got != tc.want {
			t.Errorf("en elapsedLabel(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}
