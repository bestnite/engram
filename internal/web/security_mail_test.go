package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是（A 类事务安全邮件）的验收测试。
//
// 覆盖：五类各自的触发点、重置链接一次性与过期、库中只存摘要、明文不进日志、
// A 类不携带退订头、SMTP 未配置时流程禁用。页面已切到 SPA，触发点全部走同源 JSON 端点
// （/api/v1/auth/*、/api/v1/settings/*）。
//
// 测试用一个记录型 Sender 承接入队的邮件，从而不依赖真实 SMTP；投递本身由
// outbox worker 负责，这里只验证「触发点确实把正确类型的邮件写进了队列」。

// recordingSender 是 mail.Sender 的测试替身：把每次投递的邮件记下来，永远成功。
type recordingSender struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (r *recordingSender) Send(_ context.Context, _ mail.SMTPConfig, _ mail.Message) error {
	return nil
}

// Test 实现 mail.Sender 的握手方法；测试替身总是成功。
func (r *recordingSender) Test(_ context.Context, _ mail.SMTPConfig) error { return nil }

// securityTestServer 汇总一次测试所需的句柄。
type securityTestServer struct {
	srv     *Server
	db      *gorm.DB
	ownerID uint64
	cookies []*http.Cookie
	csrf    string
	logs    *bytes.Buffer
}

// newSecurityServer 装配一个带账号、会话、TOTP 与邮件 outbox 的测试服务，并登录 owner。
// withMail 为 false 时故意不装配 outbox，用来验证「SMTP 未配置时流程禁用」。
func newSecurityServer(t *testing.T, withMail bool) securityTestServer {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "security.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	codec, err := store.NewSecretCodec(testEncryptionKey())
	if err != nil {
		t.Fatalf("NewSecretCodec() error = %v", err)
	}
	totpSvc, err := auth.NewTOTPService(store.NewTOTPStore(db), codec, auth.DefaultTOTPIssuer)
	if err != nil {
		t.Fatalf("NewTOTPService() error = %v", err)
	}

	// SMTP 已配置：settings 里有 host 与 from，Outbox.Configured() 才会为真。
	now := time.Now().UTC()
	for k, v := range map[string]string{"smtp_host": "smtp.test", "smtp_from": "noreply@test"} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
	var outbox *mail.Outbox
	if withMail {
		outbox = mail.NewOutbox(mail.Deps{
			DB:      db,
			Secrets: codec,
			Logger:  discardLogger(),
			Sender:  &recordingSender{},
		})
	}

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv, err := New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        logger,
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Auditor:       auditor,
		TOTP:          totpSvc,
		Mail:          outbox,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!",
		Role: store.RoleAdmin, Locale: "en",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	// 登录走同源 JSON 端点：会话 cookie 与会话绑定的 CSRF token 一并取回。
	cookies, csrf := loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	return securityTestServer{srv: srv, db: db, ownerID: srvOwnerID(owner), cookies: cookies, csrf: csrf, logs: logs}
}

func srvOwnerID(u *store.User) uint64 { return u.ID }

// outboxByType 返回队列里指定类型的邮件行（按 id 升序）。
func outboxByType(t *testing.T, db *gorm.DB, typ mail.Type) []store.OutboxMessage {
	t.Helper()
	var rows []store.OutboxMessage
	if err := db.Where("type = ?", string(typ)).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("query outbox by type: %v", err)
	}
	return rows
}

// tokenFromBody 从邮件正文里抽出链接里的明文令牌。
func tokenFromBody(t *testing.T, body string) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no token found in mail body: %s", snippet(body))
	}
	return m[1]
}

// assertNoUnsubscribeHeader 断言这些 A 类邮件都不带退订头。
// SecurityNotifier 从不写 Headers，因此 headers_json 是空对象，也绝不出现
// List-Unsubscribe / List-Unsubscribe-Post。
func assertNoUnsubscribeHeader(t *testing.T, rows []store.OutboxMessage) {
	t.Helper()
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.HeadersJSON), "unsubscribe") {
			t.Errorf("class A mail %d (%s) carries an unsubscribe header: %s", row.ID, row.Type, row.HeadersJSON)
		}
		raw := strings.TrimSpace(row.HeadersJSON)
		if raw == "" || raw == "{}" || raw == "null" {
			continue
		}
		var headers map[string]string
		if err := json.Unmarshal([]byte(raw), &headers); err != nil {
			t.Errorf("mail %d headers_json is not a JSON object: %q", row.ID, raw)
			continue
		}
		for k := range headers {
			if strings.EqualFold(k, "List-Unsubscribe") || strings.EqualFold(k, "List-Unsubscribe-Post") {
				t.Errorf("class A mail %d (%s) carries unsubscribe header %q", row.ID, row.Type, k)
			}
		}
	}
}

// issueResetToken 走真实触发点 POST /api/v1/auth/forgot-password，返回邮件正文里的明文令牌与那封邮件。
func issueResetToken(t *testing.T, ts securityTestServer) (string, store.OutboxMessage) {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypePasswordReset)
	if len(rows) == 0 {
		t.Fatal("no password_reset mail enqueued after the reset request")
	}
	last := rows[len(rows)-1]
	return tokenFromBody(t, last.TextBody), last
}

// resetPasswordJSON 用一次性令牌经 JSON 端点设置新密码，返回响应。
func resetPasswordJSON(t *testing.T, ts securityTestServer, token, password string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/reset-password")
	return postJSON(ts.srv, "/api/v1/auth/reset-password",
		map[string]string{"token": token, "password": password}, []*http.Cookie{cookie}, headers)
}

// resendVerificationJSON 重发验证邮件（会话端点）。
func resendVerificationJSON(t *testing.T, ts securityTestServer) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(ts.srv, "/api/v1/settings/verify-email", nil, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
}

// verifyEmailJSON 消费验证令牌（免登录，双提交 CSRF）。
func verifyEmailJSON(t *testing.T, ts securityTestServer, token string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, headers := preSessionPair(t, ts.srv, "/verify-email")
	return postJSON(ts.srv, "/api/v1/auth/verify-email",
		map[string]string{"token": token}, []*http.Cookie{cookie}, headers)
}

// createTargetUser 直接落一个可被管理员操作的普通用户行（不依赖注册策略）。
func createTargetUser(t *testing.T, ts securityTestServer) uint64 {
	t.Helper()
	u := &store.User{
		Username: "target", Email: "target@example.com", DisplayName: "Target",
		Role: store.RoleUser, Status: store.StatusActive, Locale: "en",
		Timezone: "UTC", CreatedAt: time.Now().UTC(),
	}
	if err := store.NewUserStore(ts.db).Create(context.Background(), u); err != nil {
		t.Fatalf("create target user: %v", err)
	}
	return u.ID
}

// —— 密码重置 ——

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_\-]+)`)

// TestSecurityMailFiveClassATypesDeliveredAtTriggers 是五类 A 类邮件的主验收：
// 逐一触发五个业务动作，断言 outbox 里出现对应 mail.Type 的行，且都不带退订头。
func TestSecurityMailFiveClassATypesDeliveredAtTriggers(t *testing.T) {
	ts := newSecurityServer(t, true)

	// 1) 新设备 / 新 IP 登录提醒：fixture 的登录本身就是触发点。
	deviceRows := outboxByType(t, ts.db, mail.TypeNewDeviceLogin)
	if len(deviceRows) != 1 {
		t.Errorf("new_device_login rows after a fresh login = %d, want 1", len(deviceRows))
	}
	assertNoUnsubscribeHeader(t, deviceRows)

	// 2) 密码重置。
	_, resetRow := issueResetToken(t, ts)
	assertNoUnsubscribeHeader(t, []store.OutboxMessage{resetRow})

	// 3) 邮箱验证（重发验证邮件，需登录 + 会话绑定的 CSRF）。
	verify := resendVerificationJSON(t, ts)
	if verify.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/settings/verify-email status = %d, want 200 (body %s)", verify.Code, snippet(verify.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(verifyRows) != 1 {
		t.Errorf("email_verification rows after resend = %d, want 1", len(verifyRows))
	}
	assertNoUnsubscribeHeader(t, verifyRows)

	// 4) 凭据变更通知（改密码）—— SSR 表单已随页面层删除，改走 SPA 使用的 JSON 端点。
	change := jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, ts.cookies, ts.csrf)
	if change.Code != http.StatusNoContent {
		t.Fatalf("PATCH /api/v1/settings/password status = %d, want 204 (body %s)", change.Code, snippet(change.Body.String()))
	}
	credRows := outboxByType(t, ts.db, mail.TypeCredentialChanged)
	if len(credRows) != 1 {
		t.Errorf("credential_changed rows after a password change = %d, want 1", len(credRows))
	}
	assertNoUnsubscribeHeader(t, credRows)

	// 5) 账号被禁用通知（管理员禁用另一个账号）。
	targetID := createTargetUser(t, ts)
	disable := jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatUint(targetID, 10)+"/status",
		`{"action":"disable"}`, ts.cookies, ts.csrf)
	if disable.Code != http.StatusNoContent {
		t.Fatalf("POST /api/v1/admin/users/:id/status status = %d, want 204 (body %s)", disable.Code, snippet(disable.Body.String()))
	}
	statusRows := outboxByType(t, ts.db, mail.TypeAccountStatus)
	if len(statusRows) != 1 {
		t.Fatalf("account_status rows after disabling a user = %d, want 1", len(statusRows))
	}
	if statusRows[0].To != "target@example.com" {
		t.Errorf("account_status mail recipient = %q, want target@example.com", statusRows[0].To)
	}
	assertNoUnsubscribeHeader(t, statusRows)
}

// TestSecurityMailResetLinkIsSingleUse 是反面用例：重置链接只能用一次，二次使用被拒。
func TestSecurityMailResetLinkIsSingleUse(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	// 首次：提交成功，且新密码真的生效（能登录）。
	first := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if first.Code != http.StatusOK {
		t.Fatalf("first reset submit status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	if !strings.Contains(first.Body.String(), `"reset":true`) {
		t.Errorf("first reset did not report success: %s", snippet(first.Body.String()))
	}
	loginJSON(t, ts.srv, ts.db, "owner", "N3wSup3rSecret!")

	// 二次使用同一令牌：必须被拒（一次性）。
	second := resetPasswordJSON(t, ts, plain, "An0therSecret!")
	if second.Code != http.StatusBadRequest || apiErrorCode(t, second) != "token_used" {
		t.Fatalf("second reset submit = %d %s, want 400 token_used (body %s)", second.Code, apiErrorCode(t, second), snippet(second.Body.String()))
	}
}

// TestSecurityMailResetLinkExpires 是反面用例：过期的重置链接被拒。
func TestSecurityMailResetLinkExpires(t *testing.T) {
	ts := newSecurityServer(t, true)
	// 直接签发一枚已经过期的令牌：Issue 接收 TTL，负值即已过期。
	plain, err := ts.srv.tokens.Issue(context.Background(), ts.ownerID, store.ActionTokenPasswordReset, "", -time.Minute)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}
	rec := resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	if rec.Code != http.StatusBadRequest || apiErrorCode(t, rec) != "token_expired" {
		t.Fatalf("expired reset submit = %d %s, want 400 token_expired (body %s)", rec.Code, apiErrorCode(t, rec), snippet(rec.Body.String()))
	}
}

// TestSecurityMailResetTokenStoredAsDigestOnly 是反面用例：库里只存摘要，绝无明文令牌。
func TestSecurityMailResetTokenStoredAsDigestOnly(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	var rows []store.ActionToken
	if err := ts.db.Find(&rows).Error; err != nil {
		t.Fatalf("load action tokens: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no action_tokens row written for a password reset")
	}
	want := store.HashActionToken(plain)
	found := false
	for _, row := range rows {
		if row.TokenHash == plain {
			t.Errorf("action token %d stores the plaintext token verbatim", row.ID)
		}
		if row.TokenHash == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no action_tokens row holds sha256(%s); rows = %+v", plain, rows)
	}
	if strings.Contains(fmt.Sprintf("%+v", rows), plain) {
		t.Errorf("the plaintext token appears in the stored action_tokens rows: %+v", rows)
	}
}

// TestSecurityMailTokenNeverLogged 是反面用例：明文令牌绝不进日志。
func TestSecurityMailTokenNeverLogged(t *testing.T) {
	ts := newSecurityServer(t, true)
	plain, _ := issueResetToken(t, ts)

	// 再走一遍消费路径（成功 + 二次失败），确保失败分支也不打明文。
	resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")
	resetPasswordJSON(t, ts, plain, "N3wSup3rSecret!")

	if logs := ts.logs.String(); strings.Contains(logs, plain) {
		t.Errorf("the plaintext reset token appears in the server log:\n%s", snippet(logs))
	}
}

// TestSecurityMailClassAHasNoUnsubscribeHeader 是反面用例：五类 A 类邮件都不携带退订头。
func TestSecurityMailClassAHasNoUnsubscribeHeader(t *testing.T) {
	ts := newSecurityServer(t, true)
	// 触发全部五类。
	_, _ = issueResetToken(t, ts)
	resendVerificationJSON(t, ts)
	jsonRequest(t, ts.srv, http.MethodPatch, "/api/v1/settings/password",
		`{"old_password":"Sup3rSecret!","new_password":"N3wSup3rSecret!"}`, ts.cookies, ts.csrf)
	targetID := createTargetUser(t, ts)
	jsonRequest(t, ts.srv, http.MethodPost, "/api/v1/admin/users/"+strconv.FormatUint(targetID, 10)+"/status",
		`{"action":"disable"}`, ts.cookies, ts.csrf)

	for _, typ := range []mail.Type{
		mail.TypePasswordReset, mail.TypeEmailVerification, mail.TypeNewDeviceLogin,
		mail.TypeCredentialChanged, mail.TypeAccountStatus,
	} {
		rows := outboxByType(t, ts.db, typ)
		if len(rows) == 0 {
			t.Errorf("no %s mail enqueued; cannot assert its headers", typ)
			continue
		}
		assertNoUnsubscribeHeader(t, rows)
	}
}

// TestSecurityMailEmailVerifyAndChangeFlow 覆盖邮箱验证与改邮箱确认两条链路（JSON）。
func TestSecurityMailEmailVerifyAndChangeFlow(t *testing.T) {
	ts := newSecurityServer(t, true)

	// 邮箱验证：重发 → 消费令牌 → email_verified_at 落值；令牌一次性。
	resend := resendVerificationJSON(t, ts)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend verification status = %d, want 200 (body %s)", resend.Code, snippet(resend.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	plain := tokenFromBody(t, verifyRows[len(verifyRows)-1].TextBody)
	ok := verifyEmailJSON(t, ts, plain)
	if ok.Code != http.StatusOK {
		t.Fatalf("verify-email status = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.EmailVerifiedAt == nil {
		t.Error("email_verified_at is still NULL after consuming the verification link")
	}
	again := verifyEmailJSON(t, ts, plain)
	if again.Code != http.StatusBadRequest || apiErrorCode(t, again) != "token_used" {
		t.Errorf("reused verification token = %d %s, want 400 token_used", again.Code, apiErrorCode(t, again))
	}

	// 改邮箱：向新地址发确认信，确认前不改库。
	change := postJSON(ts.srv, "/api/v1/settings/email",
		map[string]string{"email": "new-owner@example.com"}, ts.cookies, map[string]string{auth.CSRFHeaderName: ts.csrf})
	if change.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/settings/email status = %d, want 200 (body %s)", change.Code, snippet(change.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	changeRows := rows[len(rows)-1]
	if changeRows.To != "new-owner@example.com" {
		t.Errorf("email change confirmation recipient = %q, want new-owner@example.com", changeRows.To)
	}
	// 确认前库里的邮箱不变。
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "owner@example.com" {
		t.Fatalf("email changed before confirmation: %q", owner.Email)
	}
	changeToken := tokenFromBody(t, changeRows.TextBody)
	cc, ch := preSessionPair(t, ts.srv, "/confirm-email-change")
	confirm := postJSON(ts.srv, "/api/v1/auth/confirm-email-change",
		map[string]string{"token": changeToken}, []*http.Cookie{cc}, ch)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm-email-change status = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "new-owner@example.com" {
		t.Errorf("email after confirmation = %q, want new-owner@example.com", owner.Email)
	}
}

// TestSecurityMailNewDeviceLoginDeduped 断言同一指纹重复登录不再提醒（避免每登录一次发一封）。
func TestSecurityMailNewDeviceLoginDeduped(t *testing.T) {
	ts := newSecurityServer(t, true)
	before := len(outboxByType(t, ts.db, mail.TypeNewDeviceLogin))
	// 同一 httptest 客户端（同 IP + 同 User-Agent）再登录一次。
	loginJSON(t, ts.srv, ts.db, "owner", "Sup3rSecret!")
	after := len(outboxByType(t, ts.db, mail.TypeNewDeviceLogin))
	if after != before {
		t.Errorf("new_device_login rows %d -> %d after an identical second login, want no new mail", before, after)
	}
}

// TestSecurityMailDisabledWhenSMTPNotConfigured 断言 SMTP 未配置时流程禁用并说明原因，绝不静默。
// SPA 依据响应的 mail_ready=false 渲染「本站未开启邮件功能」，服务端不再渲染说明页。
func TestSecurityMailDisabledWhenSMTPNotConfigured(t *testing.T) {
	ts := newSecurityServer(t, false)
	cookie, headers := preSessionPair(t, ts.srv, "/forgot-password")
	rec := postJSON(ts.srv, "/api/v1/auth/forgot-password",
		map[string]string{"email": "owner@example.com"}, []*http.Cookie{cookie}, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var res struct {
		MailReady bool `json:"mail_ready"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.MailReady {
		t.Error("mail_ready = true while SMTP is unconfigured")
	}
	if rows := outboxByType(t, ts.db, mail.TypePasswordReset); len(rows) != 0 {
		t.Errorf("password_reset mail enqueued while SMTP is unconfigured: %d rows", len(rows))
	}
}
