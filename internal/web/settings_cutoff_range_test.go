package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 日切点 0 是午夜；空配置才是默认 04:00，表单与存储不得混淆二者。

// cutoffInputValue 从设置页 HTML 里取出切点输入框当前的 value。
// 断言的是渲染出来的控件本身，而不是 handler 的中间变量，避免「显示对、控件错」漏网。
func cutoffInputValue(t *testing.T, body string) string {
	t.Helper()
	re := regexp.MustCompile(`id="settings-cutoff"[^>]*value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("settings page has no cutoff input: %s", snippet(body))
	}
	return m[1]
}

// postRawCutoff 提交原始字符串形式的切点，覆盖 "-1" 这类 strconv.Atoi 能解析但越界的值，
// 以及 "" 这类解析失败的值。
func postRawCutoff(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, raw string) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(t, srv, "/settings/profile", url.Values{
		"csrf_token":      {csrf},
		"display_name":    {"Owner"},
		"locale":          {"zh-CN"},
		"timezone":        {"UTC"},
		"day_cutoff_hour": {raw},
	}, cookies)
}

func TestSettingsCutoffRangeRejectsOutOfRange(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	users := store.NewUserStore(db)

	rejected := []struct{ name, raw string }{
		{"above range", "24"},
		{"negative", "-1"},
		{"not a number", "invalid"},
	}
	for _, tc := range rejected {
		t.Run("reject "+tc.name, func(t *testing.T) {
			// 先落一个合法基线，用来证明被拒的提交确实没有改动存量值。
			if rec := postProfile(t, srv, cookies, csrf, "Owner", "zh-CN", "UTC", 4); rec.Code != http.StatusSeeOther {
				t.Fatalf("baseline POST status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			rec := postRawCutoff(t, srv, cookies, csrf, tc.raw)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST cutoff=%q status = %d, want 400 (body %s)", tc.raw, rec.Code, snippet(rec.Body.String()))
			}
			u, err := users.ByID(t.Context(), ownerID)
			if err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if u.DayCutoffHour == nil || *u.DayCutoffHour != 4 {
				t.Fatalf("rejected cutoff=%q changed stored cutoff to %d, want 4", tc.raw, store.ResolveCutoff(u.DayCutoffHour))
			}
		})
	}
}

func TestSettingsCutoffRangeAcceptsBoundaries(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	users := store.NewUserStore(db)

	accepted := []struct {
		name string
		raw  string
		want int
	}{
		{"midnight", "0", 0},
		{"lower boundary", "1", 1},
		{"upper boundary", "23", 23},
	}
	for _, tc := range accepted {
		t.Run("accept "+tc.name, func(t *testing.T) {
			rec := postRawCutoff(t, srv, cookies, csrf, tc.raw)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST cutoff=%q status = %d, want 303 (body %s)", tc.raw, rec.Code, snippet(rec.Body.String()))
			}
			u, err := users.ByID(t.Context(), ownerID)
			if err != nil {
				t.Fatalf("reload user: %v", err)
			}
			if u.DayCutoffHour == nil || *u.DayCutoffHour != tc.want {
				t.Fatalf("stored cutoff = %d, want %d", store.ResolveCutoff(u.DayCutoffHour), tc.want)
			}
		})
	}
}

// TestSettingsLegacyZeroCutoffShowsMidnight 钉住旧数据的行为：库里已经是 0 的
// 用户现在按午夜读取，且页面不能再显示默认 04:00。
func TestSettingsLegacyZeroCutoffShowsMidnight(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /settings 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退设置页（DESIGN.md §8.5）。
	srv.spa = nil

	// 模拟修复前落库的旧数据：day_cutoff_hour = 0。
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("day_cutoff_hour", 0).Error; err != nil {
		t.Fatalf("seed legacy cutoff: %v", err)
	}
	if got := store.NormalizedCutoff(0); got != 0 {
		t.Fatalf("read-path rule changed: NormalizedCutoff(0) = %d, want %d", got, 0)
	}

	rec := getWithCookies(t, srv, "/settings", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := cutoffInputValue(t, rec.Body.String()); got != strconv.Itoa(0) {
		t.Fatalf("form shows cutoff %q for stored 0, want 0 (midnight)", got)
	}
}
