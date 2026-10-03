package web

import (
	"math"
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
)

// intervalLabel 把「距下次到期还有多久」渲染成本地化文案（Anki 式的 "10 分钟"、"3 天"）。
//
// 学习步骤以分钟计（默认预设 `1m,10m`），所以这里必须能表达亚天级的等待：它正是
// "刚答完几分钟后这张卡又回来"这件事的可见化（DESIGN.md §8.2）。
func intervalLabel(loc *i18n.Localizer, wait time.Duration) string {
	if wait <= 0 {
		return loc.T("review.interval.under_minute")
	}
	minutes := wait.Minutes()
	var unit string
	var count int
	switch {
	case minutes < 1:
		return loc.T("review.interval.under_minute")
	case minutes < 60:
		unit, count = "minutes", int(math.Round(minutes))
	case minutes < 24*60:
		unit, count = "hours", int(math.Round(minutes/60))
	case minutes < 30*24*60:
		unit, count = "days", int(math.Round(minutes/(24*60)))
	case minutes < 365*24*60:
		unit, count = "months", int(math.Round(minutes/(30*24*60)))
	default:
		unit, count = "years", int(math.Round(minutes/(365*24*60)))
	}
	// 取整可能把数字顶过单位边界（59.6 分 → "60 分钟"），上提一级更自然。
	switch {
	case unit == "minutes" && count >= 60:
		unit, count = "hours", 1
	case unit == "hours" && count >= 24:
		unit, count = "days", 1
	case unit == "days" && count >= 30:
		unit, count = "months", 1
	case unit == "months" && count >= 12:
		unit, count = "years", 1
	}
	if count < 1 {
		count = 1
	}
	return loc.Tf("review.interval."+unit, map[string]any{"count": count})
}
