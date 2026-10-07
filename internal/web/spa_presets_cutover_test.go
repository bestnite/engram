package web

import (
	"net/http"
	"testing"
)

// 本文件是调度预设页切流到 SPA 的验收：GET /presets 一律返回应用壳（SSR 页面层已删除，
// 不再有降级回退）。
//
// 迁移不改动授权判定：未登录一律重定向登录页；写操作走 /api/v1/presets* 的会话 JSON 端点。

// TestSPAPresetsRouteCutover 覆盖 GET /presets 的切流。
func TestSPAPresetsRouteCutover(t *testing.T) {
	srv, _, _, cookies, _, _ := newPresetsServer(t)

	assertServesSPAShell(t, getWithCookies(t, srv, "/presets", cookies), "GET /presets")

	// 未登录仍然重定向登录页（授权判定不变）。
	anon := getWithCookies(t, srv, "/presets", nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login" {
		t.Errorf("anonymous GET /presets = %d %q, want 303 /login", anon.Code, anon.Header().Get("Location"))
	}
}
