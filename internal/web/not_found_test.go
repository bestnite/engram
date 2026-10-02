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

// TestNotFoundRendersLocalizedPage 断言未知 GET 路径返回 404（不是 200），
// 且渲染的是本地化后的 HTML 页面。
func TestNotFoundRendersLocalizedPage(t *testing.T) {
	srv := newRenderServer(t, nil)

	rec := get(t, srv, unknownPath, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET %s status = %d, want 404 (body %s)", unknownPath, rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `lang="zh-CN"`) {
		t.Errorf("page does not declare lang=\"zh-CN\"; body = %s", snippet(body))
	}
	if !strings.Contains(body, "页面未找到") {
		t.Errorf("page is missing the localized heading; body = %s", snippet(body))
	}
	if !strings.Contains(body, "返回首页") {
		t.Errorf("page is missing the localized home action; body = %s", snippet(body))
	}
	if !strings.Contains(body, `href="https://git.nite07.com/nite/engram"`) {
		t.Errorf("page is missing the repository footer link; body = %s", snippet(body))
	}
	if !strings.Contains(body, `href="/no-such-page-zz9?lang=en"`) {
		t.Errorf("page is missing the language selector link for the current path; body = %s", snippet(body))
	}
}

func TestNotFoundAuthenticatedPageIncludesSettingsEntry(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, unknownPath, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET %s status = %d, want 404", unknownPath, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `href="/settings"`) {
		t.Errorf("authenticated 404 page does not link to personal settings: %s", snippet(rec.Body.String()))
	}
}

func TestNotFoundLocalizesByAcceptLanguage(t *testing.T) {
	srv := newRenderServer(t, nil)

	en := get(t, srv, unknownPath, map[string]string{"Accept-Language": "en-US,en;q=0.9"})
	if en.Code != http.StatusNotFound {
		t.Fatalf("GET %s (en) status = %d, want 404", unknownPath, en.Code)
	}
	body := en.Body.String()
	if !strings.Contains(body, "Page not found") {
		t.Errorf("en response is missing the English heading; body = %s", snippet(body))
	}
	if !strings.Contains(body, "Back to home") {
		t.Errorf("en response is missing the English home action; body = %s", snippet(body))
	}
	if strings.Contains(body, "页面未找到") {
		t.Errorf("en response still contains the Chinese heading")
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

// TestNotFoundKeepsMachineAndNonGETPathsPlain 断言机器接口路径（/api、/mcp）与非 GET
// 请求不回 HTML 页面：/api 走统一 JSON 错误包壳，/mcp 与非 GET 只回朴素 404 状态。
func TestNotFoundKeepsMachineAndNonGETPathsPlain(t *testing.T) {
	srv := newRenderServer(t, nil)

	apiRec := get(t, srv, "/api/v1/no-such-endpoint", nil)
	if apiRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/no-such-endpoint status = %d, want 404", apiRec.Code)
	}
	if ct := apiRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("API 404 Content-Type = %q, want application/json", ct)
	}
	if strings.Contains(apiRec.Body.String(), "页面未找到") {
		t.Errorf("API path rendered the HTML 404 page; body = %s", snippet(apiRec.Body.String()))
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
		t.Errorf("API 404 error.code = %q, want %q", apiBody.Error.Code, "not_found")
	}
	if apiBody.Error.Message == "" {
		t.Errorf("API 404 error.message is empty; body = %s", apiRec.Body.String())
	}

	mcpRec := get(t, srv, "/mcp/no-such-endpoint", nil)
	if mcpRec.Code != http.StatusNotFound {
		t.Fatalf("GET /mcp/no-such-endpoint status = %d, want 404", mcpRec.Code)
	}
	if strings.Contains(mcpRec.Body.String(), "页面未找到") {
		t.Errorf("MCP path rendered the HTML 404 page; body = %s", snippet(mcpRec.Body.String()))
	}

	req := httptest.NewRequest(http.MethodPost, unknownPath, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST %s status = %d, want 404", unknownPath, rec.Code)
	}
	if strings.Contains(rec.Body.String(), "页面未找到") {
		t.Errorf("non-GET path rendered the HTML 404 page; body = %s", snippet(rec.Body.String()))
	}
}
