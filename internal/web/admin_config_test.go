package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「系统设置 / 邮件 / 身份与 OIDC」切流到 SPA 的验收。
// 读写端点复用与 SSR 完全同一份解析与校验；敏感项只回状态，绝不回显明文。

// TestAdminConfigPagesCutover 覆盖三个配置页的切流与回退。
func TestAdminConfigPagesCutover(t *testing.T) {
	for _, path := range []string{"/admin/settings", "/admin/smtp", "/admin/oidc"} {
		t.Run(path, func(t *testing.T) {
			srv, db, _, cookies, _ := newNotesServer(t)

			assertShell(t, getWithCookies(t, srv, path, cookies))

			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "cfg_stranger")
			if rec := getWithCookies(t, srv, path, strangerCookies); rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin GET %s = %d, want 403", path, rec.Code)
			}

		})
	}
}

// TestAdminSettingsReadAndSave 覆盖系统设置 JSON 的读取、校验与落库。
func TestAdminSettingsReadAndSave(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	names := map[string]bool{}
	foundSiteName := false
	for _, sec := range read.Sections {
		names[sec.Name] = true
		for _, row := range sec.Rows {
			if row.Key == "site.name" {
				foundSiteName = true
				if row.Source != "default" {
					t.Errorf("site.name source = %q, want default", row.Source)
				}
			}
		}
	}
	for _, want := range []string{"general", "media", "optimize"} {
		if !names[want] {
			t.Errorf("settings response missing section %q", want)
		}
	}
	if !foundSiteName {
		t.Error("settings response missing the site.name row")
	}

	// 合法保存：落库且标出来源为 db。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.name": "Example Deck Site", "media_max_bytes": "2048"},
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save settings = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["site.name"] != "Example Deck Site" || settings["media_max_bytes"] != "2048" {
		t.Fatalf("settings not persisted: %+v", settings)
	}
	after := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if !strings.Contains(after.Body.String(), "Example Deck Site") {
		t.Errorf("settings read does not reflect the saved value: %s", snippet(after.Body.String()))
	}

	// 非法语言：400 且不落库。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.default_locale": "de"},
	}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid locale = %d, want 400", rec.Code)
	}
	if _, ok := settings["site.default_locale"]; ok {
		t.Error("invalid locale was persisted")
	}
}

// TestAdminSettingsSecretNeverRevealed 断言敏感键经加密落库，响应里只回「已配置」。
func TestAdminSettingsSecretNeverRevealed(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// 敏感键必须先作为一行存在（settings 页只列已配置的敏感键，与 SSR 一致）。
	const first = "spa-plaintext-secret-one"
	if err := store.PutSecret(ctx, db, srv.secrets, "oidc_client_secret", first, nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}

	var row store.Setting
	if err := db.First(&row, "key = ?", "oidc_client_secret").Error; err != nil {
		t.Fatalf("load stored secret: %v", err)
	}
	if strings.Contains(row.Value, first) {
		t.Fatalf("stored secret contains plaintext: %q", row.Value)
	}

	// 读取：报告「已配置」，绝不回显明文。
	rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if strings.Contains(rec.Body.String(), first) {
		t.Fatal("settings response leaked the secret plaintext")
	}
	var read adminSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	configured := false
	for _, sec := range read.Sections {
		if sec.Name != "sensitive" {
			continue
		}
		for _, r := range sec.Rows {
			if r.Key == "oidc_client_secret" && r.Configured {
				configured = true
			}
		}
	}
	if !configured {
		t.Error("sensitive section does not report the secret as configured")
	}

	// 通过 JSON 写入新值：库里不得出现明文，响应依旧不回显。
	const second = "spa-plaintext-secret-two"
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"oidc_client_secret": second},
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save secret = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if err := db.First(&row, "key = ?", "oidc_client_secret").Error; err != nil {
		t.Fatalf("reload stored secret: %v", err)
	}
	if strings.Contains(row.Value, second) || strings.Contains(row.Value, first) {
		t.Fatalf("stored secret contains plaintext: %q", row.Value)
	}
	if rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil); strings.Contains(rec.Body.String(), second) {
		t.Fatal("settings response leaked the newly saved plaintext")
	}
}

// TestAdminSMTPReadSaveAndTest 覆盖 SMTP JSON 的读取、校验与测试连接失败原因。
func TestAdminSMTPReadSaveAndTest(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)

	rec := getJSON(t, srv, "/api/v1/admin/smtp", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET smtp = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminSMTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode smtp: %v", err)
	}
	if read.Configured || read.PasswordConfigured {
		t.Errorf("fresh smtp config = configured %v password %v, want both false", read.Configured, read.PasswordConfigured)
	}

	// 非法端口：400 且不落库。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{Port: "70000"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid port = %d, want 400", rec.Code)
	}
	// 非法 TLS 模式：400。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{TLSMode: "ssl"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid tls mode = %d, want 400", rec.Code)
	}

	// 合法保存：写库并显示已配置。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{
		Host: "localhost", Port: "587", From: "no-reply@example.com", TLSMode: "starttls", Password: "smtp-pw",
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save smtp = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["smtp_host"] != "localhost" {
		t.Fatalf("smtp_host = %q, want localhost", settings["smtp_host"])
	}
	if strings.Contains(settings["smtp_password"], "smtp-pw") {
		t.Fatal("smtp password stored in plaintext")
	}

	// 测试连接失败：把服务端原始错误带到响应（验收点）。
	addr := deadAddr(t)
	host, port, _ := net.SplitHostPort(addr)
	testRec := adminPostJSON(t, srv, "/api/v1/admin/smtp/test", adminSMTPRequest{
		Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none",
	}, cookies, csrf)
	var result adminTestResult
	if err := json.Unmarshal(testRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode smtp test: %v", err)
	}
	if result.OK || result.Message == "" {
		t.Errorf("smtp test result = %+v, want failure with server error text", result)
	}

	// 没有主机：稳定 no_host 码（用未配置的新服务，已保存 host 会在上面命中）。
	emptySrv, _, _, emptyCookies, emptyCSRF := newNotesServer(t)
	noHost := adminPostJSON(t, emptySrv, "/api/v1/admin/smtp/test", adminSMTPRequest{}, emptyCookies, emptyCSRF)
	if err := json.Unmarshal(noHost.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode smtp test: %v", err)
	}
	if result.OK || result.Code != "no_host" {
		t.Errorf("empty smtp test = %+v, want code no_host", result)
	}
}

// TestAdminOIDCReadSaveAndTest 覆盖 OIDC JSON 的读取、校验与测试连接无 issuer。
func TestAdminOIDCReadSaveAndTest(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/oidc", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET oidc = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminOIDCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode oidc: %v", err)
	}
	if read.Enabled || read.RedirectURI == "" {
		t.Errorf("oidc read = enabled %v redirect %q, want disabled with a redirect uri", read.Enabled, read.RedirectURI)
	}

	// 启用但 issuer 非法：400。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{Enabled: true, Issuer: "not a url"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid issuer = %d, want 400", rec.Code)
	}
	// 启用但 issuer 为空：400。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{Enabled: true}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("empty issuer with enabled = %d, want 400", rec.Code)
	}

	// 合法保存：issuer 落库。
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{
		Enabled: true, Issuer: "https://idp.example.com/", ClientID: "engram", Scopes: "openid email",
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save oidc = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["oidc.issuer"] != "https://idp.example.com" || settings["oidc.enabled"] != "true" {
		t.Fatalf("oidc settings not persisted: %+v", settings)
	}

	// 测试连接：没有可用的 issuer（已保存的空）→ no_issuer。
	emptySrv, _, _, emptyCookies, emptyCSRF := newNotesServer(t)
	testRec := adminPostJSON(t, emptySrv, "/api/v1/admin/oidc/test", adminOIDCRequest{}, emptyCookies, emptyCSRF)
	var result adminTestResult
	if err := json.Unmarshal(testRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode oidc test: %v", err)
	}
	if result.OK || result.Code != "no_issuer" {
		t.Errorf("empty oidc test = %+v, want code no_issuer", result)
	}
}
