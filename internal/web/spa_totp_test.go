package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA TOTP 管理接口（internal/web/spa_totp.go）的端到端证据：
// 走真实路由 + 真实 SQLite，复用 auth.TOTPService，断言的状态与 SSR 设置页一致。
//
// 安全断言的重点：GET /api/v1/settings/totp 绝不返回 secret / otpauth；
// secret 与恢复码明文只在 begin / confirm / recovery 的响应里各出现一次。

// spaTOTPLogin 在账号尚未启用 TOTP 时用密码登录，返回会话 cookie 与会话绑定的 CSRF token。
func spaTOTPLogin(t *testing.T, srv *Server, db *gorm.DB) ([]*http.Cookie, string) {
	t.Helper()
	rec := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	if sess.CSRFToken == "" {
		t.Fatal("session row has no CSRF token")
	}
	return rec.Result().Cookies(), sess.CSRFToken
}

// spaTOTPEnable 走 HTTP 流程启用 TOTP，返回确认时用到的明文 secret。
func spaTOTPEnable(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string) string {
	t.Helper()
	begin := postSPAJSON(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	if begin.Code != http.StatusOK {
		t.Fatalf("POST begin = %d, want 200 (body %s)", begin.Code, snippet(begin.Body.String()))
	}
	var begun spaTOTPBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.Secret == "" || !strings.Contains(begun.OtpauthURL, "otpauth://totp/") || !begun.Pending {
		t.Fatalf("begin response = %+v, want a pending secret with an otpauth link", begun)
	}
	code, err := totp.GenerateCode(begun.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	confirm := postSPAJSON(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": code}, cookies, csrf)
	if confirm.Code != http.StatusOK {
		t.Fatalf("POST confirm = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	return begun.Secret
}

// assertNoTOTPSecret 断言 GET 响应体不含 secret 或 otpauth 字段/取值。
func assertNoTOTPSecret(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "secret") || strings.Contains(body, "otpauth") {
		t.Fatalf("TOTP GET response leaked secret material: %s", snippet(body))
	}
}

// TestSPATOTPStatusAndBeginNeverLeakSecretOnGet 覆盖状态读取与 begin 的一次性 secret 语义。
func TestSPATOTPStatusAndBeginNeverLeakSecretOnGet(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := spaTOTPLogin(t, srv, db)

	// 未启用：GET 报告 false/false/0，且不含任何秘密材料。
	status := getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", status.Code, snippet(status.Body.String()))
	}
	var before spaTOTPStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Enabled || before.Pending || before.RecoveryRemaining != 0 {
		t.Fatalf("initial status = %+v, want enabled=false pending=false remaining=0", before)
	}
	assertNoTOTPSecret(t, status.Body.String())

	// begin：secret 与 otpauth 链接在此响应里一次性出现。
	begin := postSPAJSON(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	if begin.Code != http.StatusOK {
		t.Fatalf("POST begin = %d, want 200 (body %s)", begin.Code, snippet(begin.Body.String()))
	}
	var begun spaTOTPBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.Secret == "" || !strings.Contains(begun.OtpauthURL, "otpauth://totp/") || !begun.Pending {
		t.Fatalf("begin response = %+v", begun)
	}

	// 待确认：GET 只报告 pending=true，仍然不含 secret / otpauth。
	status = getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	var pending spaTOTPStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Enabled || !pending.Pending {
		t.Fatalf("pending status = %+v, want enabled=false pending=true", pending)
	}
	assertNoTOTPSecret(t, status.Body.String())

	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionTOTPBegin); err != nil || n != 1 {
		t.Fatalf("totp.begin audit = (%d, %v), want 1", n, err)
	}
}

// TestSPATOTPConfirmThenDisableRequiresPassword 覆盖启用（含恢复码一次性返回）与关闭需密码。
func TestSPATOTPConfirmThenDisableRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	cookies, csrf := spaTOTPLogin(t, srv, db)

	// 没有待确认绑定就 confirm：必须拒绝，且不启用。
	early := postSPAJSON(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": "123456"}, cookies, csrf)
	if early.Code != http.StatusBadRequest {
		t.Fatalf("confirm without a pending setup = %d, want 400 (body %s)", early.Code, snippet(early.Body.String()))
	}

	// begin 后用错误验证码 confirm：拒绝，仍未启用。
	begin := postSPAJSON(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	var begun spaTOTPBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	bad := postSPAJSON(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": "000000"}, cookies, csrf)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("confirm with a bad code = %d, want 400 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP was enabled despite a rejected code")
	}

	// 正确验证码：启用，一次性返回 10 个恢复码。
	code, err := totp.GenerateCode(begun.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	confirm := postSPAJSON(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": code}, cookies, csrf)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm with a valid code = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	var confirmed spaTOTPConfirmResponse
	if err := json.Unmarshal(confirm.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if !confirmed.Enabled || len(confirmed.RecoveryCodes) != auth.RecoveryCodeCount || confirmed.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("confirm response = %+v, want enabled with %d recovery codes", confirmed, auth.RecoveryCodeCount)
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP is not enabled after a valid confirmation")
	}

	// 已启用后的 GET：enabled=true，剩余恢复码计数正确，且不含秘密材料。
	status := getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	var after spaTOTPStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.Enabled || after.Pending || after.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("enabled status = %+v", after)
	}
	assertNoTOTPSecret(t, status.Body.String())

	// 关闭：缺 CSRF 403；密码错误 401 且仍启用；密码正确 200 后关闭。
	if rec := postSPAJSON(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("disable without CSRF = %d, want 403", rec.Code)
	}
	wrong := postSPAJSON(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "WrongPassword!"}, cookies, csrf)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP was disabled despite a wrong password")
	}
	ok := postSPAJSON(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("disable with the correct password = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP is still enabled after a confirmed disable")
	}
	again := postSPAJSON(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if again.Code != http.StatusConflict {
		t.Fatalf("disable when not enabled = %d, want 409 (body %s)", again.Code, snippet(again.Body.String()))
	}

	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionTOTPEnable); err != nil || n != 1 {
		t.Fatalf("totp.enable audit = (%d, %v), want 1", n, err)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionTOTPDisable); err != nil || n != 1 {
		t.Fatalf("totp.disable audit = (%d, %v), want 1", n, err)
	}
}

// TestSPATOTPRecoveryRegenerationRequiresPassword 覆盖恢复码重新生成需密码、旧的作废。
func TestSPATOTPRecoveryRegenerationRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	cookies, csrf := spaTOTPLogin(t, srv, db)
	spaTOTPEnable(t, srv, cookies, csrf)

	// 缺 CSRF 403；密码错误 401 且不生成。
	if rec := postSPAJSON(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("recovery without CSRF = %d, want 403", rec.Code)
	}
	wrong := postSPAJSON(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "WrongPassword!"}, cookies, csrf)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("recovery with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if n, err := srv.totp.UnusedRecoveryCodeCount(context.Background(), u.ID); err != nil || n != int64(auth.RecoveryCodeCount) {
		t.Fatalf("recovery codes after a rejected attempt = (%d, %v), want %d", n, err, auth.RecoveryCodeCount)
	}

	// 密码正确：返回一批新码，旧码立即作废（计数仍为 RecoveryCodeCount）。
	ok := postSPAJSON(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("recovery with the correct password = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var regenerated spaTOTPRecoveryResponse
	if err := json.Unmarshal(ok.Body.Bytes(), &regenerated); err != nil {
		t.Fatal(err)
	}
	if len(regenerated.RecoveryCodes) != auth.RecoveryCodeCount || regenerated.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("recovery response = %+v, want %d codes", regenerated, auth.RecoveryCodeCount)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionTOTPRecoveryRegenerate); err != nil || n != 1 {
		t.Fatalf("totp.recovery_regenerate audit = (%d, %v), want 1", n, err)
	}

	// 关闭后再重新生成恢复码：必须拒绝。
	if rec := postSPAJSON(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("disable before the not-enabled recovery check = %d, want 200", rec.Code)
	}
	notEnabled := postSPAJSON(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if notEnabled.Code != http.StatusConflict {
		t.Fatalf("recovery when not enabled = %d, want 409 (body %s)", notEnabled.Code, snippet(notEnabled.Body.String()))
	}
}

// TestSPATOTPRejectsAnonymousAndBearer 断言接口只接受浏览器会话。
func TestSPATOTPRejectsAnonymousAndBearer(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)

	if rec := getWithCookies(t, srv, "/api/v1/settings/totp", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET status = %d, want 401", rec.Code)
	}
	if rec := postSPAJSON(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous POST begin = %d, want 403 CSRF denial", rec.Code)
	}

	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: u.ID, Name: "spa totp test", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest(http.MethodGet, "/api/v1/settings/totp", nil)
	bearer.Header.Set("Authorization", "Bearer "+created.Plaintext)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, bearer)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bearer GET status = %d, want 403", rec.Code)
	}
}
