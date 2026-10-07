package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 日切点 0 是午夜；空配置（NULL）才是默认 04:00，写入与存储不得混淆二者。
// SSR 的 /settings/profile 表单已删除，资料写入改走 SPA 的 PATCH /api/v1/profile。

// patchProfileCutoff 以 JSON PATCH 提交个人资料；hour 为 nil 表示清空切点（存 NULL）。
func patchProfileCutoff(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, hour *int) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"display_name":    "Owner",
		"locale":          "zh-CN",
		"timezone":        "UTC",
		"day_cutoff_hour": hour,
	})
	if err != nil {
		t.Fatalf("marshal profile patch: %v", err)
	}
	return jsonRequest(t, srv, http.MethodPatch, "/api/v1/profile", string(raw), cookies, csrf)
}
