package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// unknownPath 是一个不与任何已注册路由匹配的路径，用于触发 NoRoute 回退。
const unknownPath = "/no-such-page-zz9"

// TestNotFoundFallbackServesIndex 断言当 SPA 已嵌入时，未知的页面型 GET 路径
// 回退 SPA 应用壳（index.html，ROADMAP Task B2），
// 返回 200 状态码并带有正确的缓存与 MIME 响应头。
func TestNotFoundFallbackServesIndex(t *testing.T) {
	srv := newRenderServer(t, nil)

	rec := get(t, srv, unknownPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 (body %s)", unknownPath, rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want containing no-cache", cc)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Errorf("ETag is missing")
	}

	body := rec.Body.String()
	if !strings.Contains(body, `<div id="app"></div>`) {
		t.Errorf("SPA index.html is missing app mount point: %s", snippet(body))
	}

	// If-None-Match 条件请求断言返回 304
	condRec := get(t, srv, unknownPath, map[string]string{"If-None-Match": etag})
	if condRec.Code != http.StatusNotModified {
		t.Errorf("conditional GET %s status = %d, want 304", unknownPath, condRec.Code)
	}
}

// TestNotFoundDoesNotShadowRegisteredRoutes 断言回退只处理未命中路由，
// 已注册路径（首页、健康检查、登录页）仍走各自 handler。
func TestNotFoundDoesNotShadowRegisteredRoutes(t *testing.T) {
	srv := newRenderServer(t, nil)

	tests := []struct {
		path string
		want int
	}{
		{path: "/", want: http.StatusOK},
		{path: "/healthz", want: http.StatusOK},
		{path: "/manifest.webmanifest", want: http.StatusOK},
	}
	for _, tc := range tests {
		rec := get(t, srv, tc.path, nil)
		if rec.Code != tc.want {
			t.Errorf("GET %s status = %d, want %d", tc.path, rec.Code, tc.want)
		}
		if body := rec.Body.String(); strings.Contains(body, "页面未找到") && tc.path != unknownPath {
			t.Errorf("GET %s rendered the 404 page", tc.path)
		}
	}
}

// TestNotFoundKeepsMachineAndNonGETPathsPlain 断言机器接口路径（/api、/mcp）、
// 静态/媒体资源未命中路径与非 GET 请求不回 HTML 页面：/api 走统一 JSON 错误包壳，
// 其余只回朴素 404 状态。
func TestNotFoundKeepsMachineAndNonGETPathsPlain(t *testing.T) {
	srv := newRenderServer(t, nil)

	apiRec := get(t, srv, "/api/v1/no-such-endpoint", nil)
	if apiRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/no-such-endpoint status = %d, want 404", apiRec.Code)
	}
	if ct := apiRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("API 404 Content-Type = %q, want application/json", ct)
	}
	if strings.Contains(apiRec.Body.String(), "页面未找到") || strings.Contains(apiRec.Body.String(), "<div id=\"app\">") {
		t.Errorf("API path rendered an HTML page; body = %s", snippet(apiRec.Body.String()))
	}
	var apiBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(apiRec.Body.Bytes(), &apiBody); err != nil {
		t.Fatalf("API 404 body is not JSON: %v (%s)", err, apiRec.Body.String())
	}
	if apiBody.Error.Code != "not_found" {
		t.Errorf("API 404 code = %q, want not_found", apiBody.Error.Code)
	}

	mcpRec := get(t, srv, "/mcp/no-such-tool", nil)
	if mcpRec.Code != http.StatusNotFound {
		t.Errorf("GET /mcp/no-such-tool status = %d, want 404", mcpRec.Code)
	}
	if mcpRec.Body.Len() != 0 {
		t.Errorf("MCP 404 wrote a body: %q", mcpRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, unknownPath, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST %s status = %d, want 404", unknownPath, rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("non-GET 404 wrote a body: %q", rec.Body.String())
	}

	// 缺失的静态资源与媒体路径必须 404，严禁回退 HTML
	staticRec := get(t, srv, "/static/nonexistent.css", nil)
	if staticRec.Code != http.StatusNotFound {
		t.Errorf("GET /static/nonexistent.css status = %d, want 404", staticRec.Code)
	}
	if strings.Contains(staticRec.Body.String(), "<div id=\"app\">") {
		t.Errorf("missing static asset fell back to SPA HTML")
	}

	assetRec := get(t, srv, "/assets/nonexistent.js", nil)
	if assetRec.Code != http.StatusNotFound {
		t.Errorf("GET /assets/nonexistent.js status = %d, want 404", assetRec.Code)
	}
	if strings.Contains(assetRec.Body.String(), "<div id=\"app\">") {
		t.Errorf("missing SPA asset fell back to SPA HTML")
	}

	mediaRec := get(t, srv, "/media/nonexistent", nil)
	if mediaRec.Code != http.StatusNotFound {
		t.Errorf("GET /media/nonexistent status = %d, want 404", mediaRec.Code)
	}
	if strings.Contains(mediaRec.Body.String(), "<div id=\"app\">") {
		t.Errorf("missing media asset fell back to SPA HTML")
	}
}
