package web

import (
	"net/http"
	"regexp"
	"strconv"
	"testing"
)

// auditTotalRE 从审计页里取出「命中： N」的 N；模板把标签与数字分两个文本节点渲染。
var auditTotalRE = regexp.MustCompile(`命中：\s*(\d+)`)

// auditTotal 请求一个审计页 URL 并解析出命中总数；解析失败时打印响应片段。
func auditTotal(t *testing.T, srv *Server, target string, cookies []*http.Cookie) int {
	t.Helper()
	rec := getWithCookies(t, srv, target, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", target, rec.Code, snippet(rec.Body.String()))
	}
	m := auditTotalRE.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("GET %s: no match count in body: %s", target, snippet(rec.Body.String()))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("GET %s: parse match count %q: %v", target, m[1], err)
	}
	return n
}
