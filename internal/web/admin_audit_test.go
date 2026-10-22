package web

import (
	"net/http"
	"regexp"
	"strconv"
	"testing"
)

// auditTotalRE extracts the N from "hits: N" on the audit page; the template renders the label and the number as two text nodes.
var auditTotalRE = regexp.MustCompile(`命中：\s*(\d+)`)

// auditTotal requests an audit page URL and parses out the total hit count; prints a response snippet when parsing fails.
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
