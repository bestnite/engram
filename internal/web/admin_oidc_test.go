package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/store"
)

// 本文件是 M6-4 的验收测试：管理面板 OIDC 配置页的「测试连接」（显示 provider 错误文本）
// 与「解绑」（解绑后该身份不能再登录）。stub provider 与辅助函数见 oidc_test.go。

// TestOIDCAdminTestConnectionShowsProviderError 是 M6-4 验收：issuer 写错时页面上出现 discovery 错误。
func TestOIDCAdminTestConnectionShowsProviderError(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	// 一个总是返回 404 且带可识别文本的“provider”。
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("provider-says-no-discovery"))
	}))
	t.Cleanup(broken.Close)

	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	srv.secrets = codec
	_ = db

	rec := postForm(t, srv, "/admin/oidc/test", url.Values{
		"csrf_token":  {csrf},
		"oidc.issuer": {broken.URL},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/oidc/test = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "provider-says-no-discovery") {
		t.Errorf("page does not surface the provider error text; body = %s", snippet(body))
	}
}

// TestOIDCAdminSaveAndUnlink 覆盖 M6-4 的保存与解绑：保存后 issuer 生效；解绑后该身份不能再登录。
func TestOIDCAdminSaveAndUnlink(t *testing.T) {
	srv, db, _, adminCookies, csrf := newNotesServer(t)
	p := newStubOIDC(t)
	p.verified = false // 不自动匹配邮箱，才能观察解绑后的拒绝
	seedOIDC(t, srv, db, p, auth.PolicyOpen)

	// 通过管理面板保存一次配置（覆盖 issuer / client id / claim 映射），并写审计。
	rec := postForm(t, srv, "/admin/oidc", url.Values{
		"csrf_token":                {csrf},
		"oidc.enabled":              {"on"},
		"oidc.issuer":               {p.srv.URL},
		"oidc.client_id":            {p.client},
		"oidc.claim.subject":        {"sub"},
		"oidc.claim.email":          {"email"},
		"oidc.claim.email_verified": {"email_verified"},
	}, adminCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/oidc = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "notice=saved") {
		t.Fatalf("POST /admin/oidc Location = %q, want notice=saved", loc)
	}
	page := getWithCookies(t, srv, "/admin/oidc", adminCookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /admin/oidc = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), "已配置") {
		t.Errorf("OIDC page does not show the client secret as configured")
	}

	// 用同一身份登录一次（policy open + 未验证邮箱 → 自动建号）。
	state, nonce := startOIDCLogin(t, srv)
	p.nonce = nonce
	if code := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, nil).Code; code != http.StatusSeeOther {
		t.Fatalf("first OIDC login = %d, want 303", code)
	}
	var ident store.Identity
	if err := db.First(&ident, "provider = ? AND subject = ?", p.srv.URL, p.sub).Error; err != nil {
		t.Fatalf("identity row not created: %v", err)
	}

	// 管理面板解绑：写审计。
	unlink := postForm(t, srv, "/admin/oidc/identities/"+strconv.FormatUint(ident.ID, 10)+"/unlink", url.Values{
		"csrf_token": {csrf},
	}, adminCookies)
	if unlink.Code != http.StatusSeeOther {
		t.Fatalf("unlink = %d, want 303 (body %s)", unlink.Code, snippet(unlink.Body.String()))
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionIdentityUnlink); err != nil || n != 1 {
		t.Errorf("identity.unlink audit rows = %d (err %v), want 1", n, err)
	}

	// 解绑后切到 closed 策略：同一身份不能再登录（既无绑定，又不允许自动建号）。
	if err := store.PutSetting(context.Background(), db, auth.SettingKeyRegistrationPolicy, auth.PolicyClosed, nil, time.Now().UTC()); err != nil {
		t.Fatalf("set policy closed: %v", err)
	}
	state2, nonce2 := startOIDCLogin(t, srv)
	p.nonce = nonce2
	denied := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state2, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("login after unlink under closed policy = %d, want 403 (body %s)", denied.Code, snippet(denied.Body.String()))
	}
	if !strings.Contains(denied.Body.String(), "联系管理员") {
		t.Errorf("denial page does not show the localized hint; body = %s", snippet(denied.Body.String()))
	}
}
