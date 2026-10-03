package web

import (
	"math"
	"strconv"

	"git.nite07.com/nite/engram/internal/i18n"
)

// elapsedLabel 渲染"已经花掉多久"，单位随量级走（DESIGN.md §9）。
//
// 累计耗时没有上界——几千条复习就是几小时——一律写毫秒会让页面出现
// "5400000 毫秒" 这种没人读得下去的数。口径：
//
//	< 1 s    → 整数毫秒
//	< 60 s   → 秒（一位小数）
//	< 60 min → 分钟（一位小数）
//	>= 1 h   → "N 小时 M 分"（M 为 0 时只写 "N 小时"）
//
// 取整会把数字顶过单位边界（59.96 秒 → 60.0 秒），此时上提一级——否则会出现
// "60 秒"、"60 分钟" 这类越级读法。别与 intervalLabel 合并：那个渲染的是"还要等
// 多久"（含学习步骤的 "<1 分钟"），词汇与舍入口径都不同。
func elapsedLabel(loc *i18n.Localizer, ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return loc.Tf("stats.duration.milliseconds", map[string]any{"value": ms})
	}
	if secs := round1(float64(ms) / 1000); secs < 60 {
		return loc.Tf("stats.duration.seconds", map[string]any{"value": decimalText(secs)})
	}
	if mins := round1(float64(ms) / 60000); mins < 60 {
		return loc.Tf("stats.duration.minutes", map[string]any{"value": decimalText(mins)})
	}
	total := int(round1(float64(ms) / 60000))
	hours, mins := total/60, total%60
	if mins == 0 {
		return loc.Tf("stats.duration.hours", map[string]any{"hours": hours})
	}
	return loc.Tf("stats.duration.hours_minutes", map[string]any{"hours": hours, "minutes": mins})
}

// round1 四舍五入到一位小数。
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// decimalText 把"一位小数"的数值渲染成文本，整值省掉 .0——中文文案里 "8 秒" 比
// "8.0 秒" 干净，而 8.4 秒这类非整值仍然保留小数。
func decimalText(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
