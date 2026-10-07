package web

import (
	"net/http"
	"strings"
	"testing"
)

// 本文件是调度预设页切流到 SPA 的验收：GET /presets 在 SPA 已加载时返回应用壳，
// 在 SPA 缺失（降级构建）时回退原有的 SSR 预设页。
//
// 迁移不改动授权判定：未登录一律重定向登录页；写操作仍留在 SSR 表单与 /api/v1/presets* 的
// 会话 JSON 端点，SSR handler 与模板全部保留。

// TestSPAPresetsRouteCutover 覆盖 GET /presets 的切流与回退。
func TestSPAPresetsRouteCutover(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)

	assertServesSPAShell(t, getWithCookies(t, srv, "/presets", cookies), "GET /presets")

	// 未登录仍然重定向登录页（授权判定不变）。
	anon := getWithCookies(t, srv, "/presets", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET /presets = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}

	// SPA 缺失时回退 SSR 预设页（新建对话框是该页面的标志性结构）。
	srv.spa = nil
	ssr := getWithCookies(t, srv, "/presets", cookies)
	if ssr.Code != http.StatusOK {
		t.Fatalf("SSR fallback GET /presets = %d, want 200 (body %s)", ssr.Code, snippet(ssr.Body.String()))
	}
	if !strings.Contains(ssr.Body.String(), `data-dialog-open="new-preset-dialog"`) {
		t.Errorf("SSR fallback did not render the presets page: %s", snippet(ssr.Body.String()))
	}
}
