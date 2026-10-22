package web

import (
	"testing"

	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestElapsedLabelUnits pins down the "duration switches unit with magnitude" rules. The boundary
// values guard both the "step up one level" behaviour (59.96 s → 1 minute, 59.98 min → 1 hour) and
// "sub-second still in milliseconds" — a single review is on that order, and seconds would call 420 ms "0.4 s".
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
		{59_960, "1 分钟"}, // 59.96 s → 60.0 s, must step up, cannot write "60 s"
		{60_000, "1 分钟"},
		{90_000, "1.5 分钟"},
		{1625_000, "27.1 分钟"},
		{3_599_000, "1 小时"}, // 59.98 min → 60.0 min, stepped up to hours
		{3_600_000, "1 小时"},
		{3_660_000, "1 小时 1 分"},
		{9_000_000, "2 小时 30 分"},
	}
	for _, tc := range cases {
		if got := elapsedLabel(zh, tc.ms); got != tc.want {
			t.Errorf("elapsedLabel(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}

	// The English language pack must carry units too (that both packs share the same keys is an i18n test matter;
	// this only confirms the template's placeholders render under the other language as well).
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
