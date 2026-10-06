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

// 待办第 1 项：日切点表单只接受 1–23。
//
// 0 在内部处处表示「未设置」（users.day_cutoff_hour 的零值、NormalizedCutoff 的回退
// 分支），把用户提交的 0 当作合法的「午夜」会让这些地方集体改义。因此表单改为拒 0、
// 只接受 1–23，且被拒的提交绝不落库；库里已有的 0 旧数据读取时仍回退 04:00。

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

func TestSettingsCutoffRangeRejectsZeroAndOutOfRange(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	users := store.NewUserStore(db)

	rejected := []struct{ name, raw string }{
		{"zero", "0"},
		{"above range", "24"},
		{"negative", "-1"},
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
			if u.DayCutoffHour != 4 {
				t.Fatalf("rejected cutoff=%q changed stored cutoff to %d, want 4", tc.raw, u.DayCutoffHour)
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
			if u.DayCutoffHour != tc.want {
				t.Fatalf("stored cutoff = %d, want %d", u.DayCutoffHour, tc.want)
			}
		})
	}
}

// TestSettingsLegacyZeroCutoffShowsEffectiveDefault 钉住旧数据的行为：库里已经是 0 的
// 用户，读取路径照旧回退 04:00（显示出来的也就是这个生效值），不因表单收紧而改变。
func TestSettingsLegacyZeroCutoffShowsEffectiveDefault(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	// 模拟修复前落库的旧数据：day_cutoff_hour = 0。
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("day_cutoff_hour", 0).Error; err != nil {
		t.Fatalf("seed legacy cutoff: %v", err)
	}
	if got := store.NormalizedCutoff(0); got != store.DefaultDayCutoffHour {
		t.Fatalf("read-path rule changed: NormalizedCutoff(0) = %d, want %d", got, store.DefaultDayCutoffHour)
	}

	rec := getWithCookies(t, srv, "/settings", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := cutoffInputValue(t, rec.Body.String()); got != strconv.Itoa(store.DefaultDayCutoffHour) {
		t.Fatalf("form shows cutoff %q for stored 0, want %q (the effective 04:00)", got, strconv.Itoa(store.DefaultDayCutoffHour))
	}
}
