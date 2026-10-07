package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
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
