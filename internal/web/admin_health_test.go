package web

import (
	"net/http"
	"regexp"
	"testing"
)

// healthValueRE extracts the value text for data-health=<key> from the health page.
var healthValueRE = regexp.MustCompile(`data-health="([a-z]+)"[^>]*>([^<]*)<`)

// healthValues requests the health page and parses all readings; prints a response snippet when parsing fails.
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
