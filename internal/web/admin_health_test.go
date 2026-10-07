package web

import (
	"net/http"
	"regexp"
	"testing"
)

// healthValueRE 从健康页取出 data-health=<key> 对应的值文本。
var healthValueRE = regexp.MustCompile(`data-health="([a-z]+)"[^>]*>([^<]*)<`)

// healthValues 请求健康页并解析出全部读数；解析失败时打印响应片段。
func healthValues(t *testing.T, srv *Server, cookies []*http.Cookie) map[string]string {
	t.Helper()
	rec := getWithCookies(t, srv, "/admin/health", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/health = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	out := make(map[string]string)
	for _, m := range healthValueRE.FindAllStringSubmatch(rec.Body.String(), -1) {
		out[m[1]] = m[2]
	}
	return out
}
