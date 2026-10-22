package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the end-to-end evidence for the SPA TOTP management API (internal/web/totp_api.go):
// real routes + real SQLite, reusing auth.TOTPService, with asserted states matching the SSR settings page.
//
// The focus of the security assertions: GET /api/v1/settings/totp never returns secret / otpauth;
// the secret and plaintext recovery codes each appear only once, in the begin / confirm / recovery responses.

// totpLogin logs in with the password over JSON while the account has no TOTP yet, returning the session cookie and the session-bound CSRF token.
func totpLogin(t *testing.T, srv *Server, db *gorm.DB) ([]*http.Cookie, string) {
	t.Helper()
	return loginJSON(t, srv, db, "admin", "Sup3rSecret!")
}

// totpEnable enables TOTP through the HTTP flow and returns the plaintext secret used for the confirmation.
func totpEnable(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string) string {
	t.Helper()
	begin := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	if begin.Code != http.StatusOK {
		t.Fatalf("POST begin = %d, want 200 (body %s)", begin.Code, snippet(begin.Body.String()))
	}
	var begun totpBeginResponse
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
	confirm := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": code}, cookies, csrf)
	if confirm.Code != http.StatusOK {
		t.Fatalf("POST confirm = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	return begun.Secret
}

// assertNoTOTPSecret asserts the GET body holds no secret or otpauth field/value.
func assertNoTOTPSecret(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "secret") || strings.Contains(body, "otpauth") {
		t.Fatalf("TOTP GET response leaked secret material: %s", snippet(body))
	}
}

// TestTOTPStatusAndBeginNeverLeakSecretOnGet covers the status read and begin's one-time secret semantics.
func TestTOTPStatusAndBeginNeverLeakSecretOnGet(t *testing.T) {
	srv, db := newAuthServer(t)
	_ = createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)

	// Not enabled: GET reports false/false/0 and holds no secret material.
	status := getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body %s)", status.Code, snippet(status.Body.String()))
	}
	var before totpStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Enabled || before.Pending || before.RecoveryRemaining != 0 {
		t.Fatalf("initial status = %+v, want enabled=false pending=false remaining=0", before)
	}
	assertNoTOTPSecret(t, status.Body.String())

	// begin: the secret and otpauth link appear once, in this response.
	begin := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	if begin.Code != http.StatusOK {
		t.Fatalf("POST begin = %d, want 200 (body %s)", begin.Code, snippet(begin.Body.String()))
	}
	var begun totpBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	if begun.Secret == "" || !strings.Contains(begun.OtpauthURL, "otpauth://totp/") || !begun.Pending {
		t.Fatalf("begin response = %+v", begun)
	}

	// Pending: GET only reports pending=true and still holds no secret / otpauth.
	status = getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	var pending totpStatusResponse
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

// TestTOTPConfirmThenDisableRequiresPassword covers enabling (including the one-time recovery codes) and the password required to disable.
func TestTOTPConfirmThenDisableRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)

	// Confirming with no pending binding: must be refused, and TOTP stays disabled.
	early := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": "123456"}, cookies, csrf)
	if early.Code != http.StatusBadRequest {
		t.Fatalf("confirm without a pending setup = %d, want 400 (body %s)", early.Code, snippet(early.Body.String()))
	}

	// Confirming with a wrong code after begin: refused, still not enabled.
	begin := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, cookies, csrf)
	var begun totpBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &begun); err != nil {
		t.Fatal(err)
	}
	bad := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": "000000"}, cookies, csrf)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("confirm with a bad code = %d, want 400 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP was enabled despite a rejected code")
	}

	// Correct code: enabled, returning 10 recovery codes once.
	code, err := totp.GenerateCode(begun.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode() error = %v", err)
	}
	confirm := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/confirm", map[string]any{"code": code}, cookies, csrf)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm with a valid code = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	var confirmed totpConfirmResponse
	if err := json.Unmarshal(confirm.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if !confirmed.Enabled || len(confirmed.RecoveryCodes) != auth.RecoveryCodeCount || confirmed.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("confirm response = %+v, want enabled with %d recovery codes", confirmed, auth.RecoveryCodeCount)
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP is not enabled after a valid confirmation")
	}

	// GET once enabled: enabled=true, the remaining recovery code count is correct, and no secret material is included.
	status := getWithCookies(t, srv, "/api/v1/settings/totp", cookies)
	var after totpStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.Enabled || after.Pending || after.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("enabled status = %+v", after)
	}
	assertNoTOTPSecret(t, status.Body.String())

	// Disable: missing CSRF 403; wrong password 401 and still enabled; correct password 200, then disabled.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("disable without CSRF = %d, want 403", rec.Code)
	}
	wrong := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "WrongPassword!"}, cookies, csrf)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("disable with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); !enabled {
		t.Fatal("TOTP was disabled despite a wrong password")
	}
	ok := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("disable with the correct password = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if enabled, _ := srv.totp.Enabled(context.Background(), u.ID); enabled {
		t.Fatal("TOTP is still enabled after a confirmed disable")
	}
	again := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
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

// TestTOTPRecoveryRegenerationRequiresPassword covers recovery-code regeneration needing the password and voiding the old codes.
func TestTOTPRecoveryRegenerationRequiresPassword(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)
	cookies, csrf := totpLogin(t, srv, db)
	totpEnable(t, srv, cookies, csrf)

	// Missing CSRF 403; wrong password 401 with nothing generated.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("recovery without CSRF = %d, want 403", rec.Code)
	}
	wrong := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "WrongPassword!"}, cookies, csrf)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("recovery with a wrong password = %d, want 401 (body %s)", wrong.Code, snippet(wrong.Body.String()))
	}
	if n, err := srv.totp.UnusedRecoveryCodeCount(context.Background(), u.ID); err != nil || n != int64(auth.RecoveryCodeCount) {
		t.Fatalf("recovery codes after a rejected attempt = (%d, %v), want %d", n, err, auth.RecoveryCodeCount)
	}

	// Correct password: a new batch of codes comes back, the old ones are void at once (the count is still RecoveryCodeCount).
	ok := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("recovery with the correct password = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var regenerated totpRecoveryResponse
	if err := json.Unmarshal(ok.Body.Bytes(), &regenerated); err != nil {
		t.Fatal(err)
	}
	if len(regenerated.RecoveryCodes) != auth.RecoveryCodeCount || regenerated.RecoveryRemaining != int64(auth.RecoveryCodeCount) {
		t.Fatalf("recovery response = %+v, want %d codes", regenerated, auth.RecoveryCodeCount)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionTOTPRecoveryRegenerate); err != nil || n != 1 {
		t.Fatalf("totp.recovery_regenerate audit = (%d, %v), want 1", n, err)
	}

	// Regenerating recovery codes after disabling: must be refused.
	if rec := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/disable", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("disable before the not-enabled recovery check = %d, want 200", rec.Code)
	}
	notEnabled := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/recovery", map[string]any{"password": "Sup3rSecret!"}, cookies, csrf)
	if notEnabled.Code != http.StatusConflict {
		t.Fatalf("recovery when not enabled = %d, want 409 (body %s)", notEnabled.Code, snippet(notEnabled.Body.String()))
	}
}

// TestTOTPRejectsAnonymousAndBearer asserts the API accepts browser sessions only.
func TestTOTPRejectsAnonymousAndBearer(t *testing.T) {
	srv, db := newAuthServer(t)
	u := createTOTPAdmin(t, srv, db)

	if rec := getWithCookies(t, srv, "/api/v1/settings/totp", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET status = %d, want 401", rec.Code)
	}
	if rec := postJSONWithCSRF(t, srv, "/api/v1/settings/totp/begin", map[string]any{}, nil, ""); rec.Code != http.StatusForbidden {
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
