package web

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestIntervalLabelPicksSensibleUnit 覆盖单位切换，以及"取整顶过单位边界"的修正
// （59.7 分不该渲染成「60 分钟」）。SPA 的复习页复用同一份格式化规则。
func TestIntervalLabelPicksSensibleUnit(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(tr.Pick("zh-CN", "", ""))
	cases := []struct {
		wait time.Duration
		want string
	}{
		{30 * time.Second, "<1 分钟"},
		{time.Minute, "1 分钟"},
		{10 * time.Minute, "10 分钟"},
		{59*time.Minute + 40*time.Second, "1 小时"},
		{2 * time.Hour, "2 小时"},
		{23*time.Hour + 50*time.Minute, "1 天"},
		{3 * 24 * time.Hour, "3 天"},
		{49 * 24 * time.Hour, "2 个月"},
		{400 * 24 * time.Hour, "1 年"},
		{800 * 24 * time.Hour, "2 年"},
	}
	for _, c := range cases {
		if got := intervalLabel(loc, c.wait); got != c.want {
			t.Errorf("intervalLabel(%v) = %q, want %q", c.wait, got, c.want)
		}
	}
}
