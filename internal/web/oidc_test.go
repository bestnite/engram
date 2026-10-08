package web

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是验收测试：用一个自写的 stub OIDC provider（httptest + 测试内生成的
// RSA 密钥）驱动完整登录流，不依赖外网或任何真实 IdP。

// stubOIDC 是一个最小 OIDC provider：发现文档、authorize、token、jwks。
// token 端点用测试内生成的 RSA 私钥签 RS256 ID Token；nonce 由测试在调用回调前注入。
type stubOIDC struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	kid      string
	client   string
	nonce    string
	sub      string
	email    string
	verified bool
}

func newStubOIDC(t *testing.T) *stubOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	p := &stubOIDC{key: key, kid: "stub-key-1", client: "stub-client",
		sub: "subject-abc", email: "stub-user@example.com", verified: true}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 p.srv.URL,
			"authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint":         p.srv.URL + "/token",
			"jwks_uri":               p.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": p.kid, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.PublicKey.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		claims := map[string]any{
			"iss":            p.srv.URL,
			"sub":            p.sub,
			"aud":            []string{p.client},
			"exp":            time.Now().Add(time.Hour).Unix(),
			"iat":            time.Now().Unix(),
			"nonce":          p.nonce,
			"email":          p.email,
			"email_verified": p.verified,
			"name":           "Stub User",
		}
		writeJSON(w, map[string]any{
			"id_token":     p.signIDToken(t, claims),
			"access_token": "stub-access-token",
			"token_type":   "Bearer",
		})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *stubOIDC) signIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": p.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign id_token: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// seedOIDC 写入一套可用的 OIDC 配置，返回加密编解码器；policy 是注册策略。
func seedOIDC(t *testing.T, srv *Server, db *gorm.DB, p *stubOIDC, policy string) *store.SecretCodec {
	t.Helper()
	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	srv.secrets = codec
	ctx := context.Background()
	now := time.Now().UTC()
	for k, v := range map[string]string{
		auth.SettingKeyOIDCEnabled:        "true",
		auth.SettingKeyOIDCIssuer:         p.srv.URL,
		auth.SettingKeyOIDCClientID:       p.client,
		auth.SettingKeyRegistrationPolicy: policy,
	} {
		if err := store.PutSetting(ctx, db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
	if err := store.PutSecret(ctx, db, codec, auth.SettingKeyOIDCClientSecret, "stub-secret", nil, now); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}
	return codec
}

// startOIDCLogin 调用 /auth/oidc/start 并解析出 state 与 nonce，同时断言 PKCE 参数存在。
func startOIDCLogin(t *testing.T, srv *Server) (state, nonce string) {
	t.Helper()
	rec := getWithCookies(t, srv, "/auth/oidc/start", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("GET /auth/oidc/start = %d, want 302 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize location: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("authorize URL is missing PKCE S256: %s", u.String())
	}
	return q.Get("state"), q.Get("nonce")
}

// TestOIDCLoginAgainstStubProvider 是核心验收：stub provider 下登录成功，错误 state 被拒。
func TestOIDCLoginAgainstStubProvider(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyOpen)

	state, nonce := startOIDCLogin(t, srv)
	p.nonce = nonce

	// 错误 state 必须被拒：SPA 已取代 SSR 登录页，失败回 303 登录页，且不下发会话。
	bad := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state=forged-state", nil)
	if bad.Code != http.StatusSeeOther {
		t.Fatalf("callback with a wrong state = %d, want 303 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if loc := bad.Header().Get("Location"); loc != "/login" {
		t.Fatalf("wrong-state redirect Location = %q, want /login", loc)
	}
	// 错误 state 必须被拒，且不下发会话 cookie。
	for _, ck := range bad.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() {
			t.Fatalf("wrong state unexpectedly set a session cookie")
		}
	}

	// 正确 state：登录成功并下发会话。
	ok := getWithCookies(t, srv, "/auth/oidc/callback?code=stub-code&state="+state, nil)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d, want 303 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if loc := ok.Header().Get("Location"); loc != "/" {
		t.Fatalf("callback Location = %q, want /", loc)
	}
	if len(ok.Result().Cookies()) == 0 {
		t.Fatal("successful OIDC login did not set a session cookie")
	}

	// 绑定关系必须落库：provider = issuer，subject = sub。
	var ident store.Identity
	if err := db.First(&ident, "provider = ? AND subject = ?", p.srv.URL, p.sub).Error; err != nil {
		t.Fatalf("identity row not created: %v", err)
	}
	// 建出来的账号必须是纯 OIDC 账号（无本地密码）。
	var u store.User
	if err := db.First(&u, "id = ?", ident.UserID).Error; err != nil {
		t.Fatalf("load linked user: %v", err)
	}
	if u.PasswordHash != nil {
		t.Error("auto-created OIDC user unexpectedly has a local password")
	}

	// 审计：登录成功 + 身份绑定各一条。
	audit := store.NewAuditStore(db)
	if n, err := audit.CountByAction(context.Background(), store.ActionIdentityLink); err != nil || n != 1 {
		t.Errorf("identity.link audit rows = %d (err %v), want 1", n, err)
	}
	if n, err := audit.CountByAction(context.Background(), store.ActionUserLoginSucceeded); err != nil || n < 1 {
		t.Errorf("user.login_succeeded audit rows = %d (err %v), want at least 1", n, err)
	}
}

// TestOIDCDisabledHidesEntryAndRoutes 验证默认关闭：登录页无入口，相关路由 404（不允许半开）。
func TestOIDCDisabledHidesEntryAndRoutes(t *testing.T) {
	srv, _ := newAuthServer(t)
	// 未配置任何 OIDC 设置。
	body := getWithCookies(t, srv, "/login", nil).Body.String()
	if strings.Contains(body, "OIDC") {
		t.Errorf("login page shows an OIDC entry while OIDC is disabled")
	}
	if code := getWithCookies(t, srv, "/auth/oidc/start", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /auth/oidc/start while disabled = %d, want 404", code)
	}
	if code := getWithCookies(t, srv, "/auth/oidc/callback?code=x&state=y", nil).Code; code != http.StatusNotFound {
		t.Errorf("GET /auth/oidc/callback while disabled = %d, want 404", code)
	}
}

// TestOIDCStartRejectsWhenPendingTableFull 是这条规则的验收：未完成的 state 表达到上限后，
// 新的 OIDC 发起必须被拒绝（303 回登录页），而不是继续往表里堆（内存 DoS）。
func TestOIDCStartRejectsWhenPendingTableFull(t *testing.T) {
	srv, db := newAuthServer(t)
	p := newStubOIDC(t)
	seedOIDC(t, srv, db, p, auth.PolicyOpen)

	// 预置一批**未过期**的待完成登录，把内存表填满（远超上限）。
	for i := 0; i < 5000; i++ {
		srv.oidc.PutPending(fmt.Sprintf("pre-seeded-%d", i), auth.PendingAuth{
			Nonce:       "nonce",
			Verifier:    "verifier",
			RedirectURI: "http://example.com/auth/oidc/callback",
			ExpiresAt:   time.Now().UTC().Add(time.Hour),
		})
	}

	rec := getWithCookies(t, srv, "/auth/oidc/start", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /auth/oidc/start with a full pending table = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("rejection redirect Location = %q, want /login", loc)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == srv.sessions.CookieName() {
			t.Fatal("rejected OIDC start unexpectedly set a session cookie")
		}
	}
}
