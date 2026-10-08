package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 的 OIDC 登录入口探测端点验收（internal/web/spa_oidc.go）。
//
// 覆盖：默认关闭时 enabled=false、配置完整可用时 enabled=true，且响应绝不含 issuer / client_id
// 等凭据；start_url 与 SSR 登录页按钮同源。

// TestOIDCEntryProbe 断言探测端点只反映「是否可用」，绝不回显任何配置凭据。
func TestOIDCEntryProbe(t *testing.T) {
	srv, db := newAuthServer(t)

	type probe struct {
		Enabled  bool   `json:"enabled"`
		StartURL string `json:"start_url"`
	}

	// 默认关闭：enabled=false，start_url 仍稳定给出（前端据此决定是否渲染入口）。
	rec := get(t, srv, "/api/v1/auth/oidc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/oidc status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var off probe
	decodeJSON(t, rec, &off)
	if off.Enabled {
		t.Errorf("oidc enabled = true by default, want false")
	}
	if off.StartURL != "/auth/oidc/start" {
		t.Errorf("start_url = %q, want /auth/oidc/start", off.StartURL)
	}

	// 配置完整可用：enabled=true。
	now := time.Now().UTC()
	for k, v := range map[string]string{
		auth.SettingKeyOIDCEnabled:  "true",
		auth.SettingKeyOIDCIssuer:   "https://issuer.example.com",
		auth.SettingKeyOIDCClientID: "client-abc",
	} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
	on := get(t, srv, "/api/v1/auth/oidc", nil)
	var enabled probe
	decodeJSON(t, on, &enabled)
	if !enabled.Enabled {
		t.Errorf("oidc enabled = false after a complete config, want true")
	}
	if body := on.Body.String(); strings.Contains(body, "issuer.example.com") || strings.Contains(body, "client-abc") {
		t.Errorf("oidc probe leaked configuration: %s", snippet(body))
	}
}
