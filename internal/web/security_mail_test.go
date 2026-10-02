package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
)

// 本文件是 M1-19（A 类事务安全邮件）的验收测试。
//
// 覆盖：五类各自的触发点、重置链接一次性与过期、库中只存摘要、明文不进日志、
// A 类不携带退订头、SMTP 未配置时流程禁用并说明原因。
//
// 测试用一个记录型 Sender 承接入队的邮件，从而不依赖真实 SMTP；投递本身由 M1-17
// 的 outbox worker 负责，这里只验证「触发点确实把正确类型的邮件写进了队列」。

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
// withMail 为 false 时故意不装配 outbox，用来验证「SMTP 未配置时流程禁用并说明原因」。
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
	accounts, err := auth.NewAccountService(users, sessions, auth.NewPasswordHasher(auth.Params{
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
	login := postForm(t, srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303 (body %s)", login.Code, snippet(login.Body.String()))
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	return securityTestServer{srv: srv, db: db, ownerID: srvOwnerID(owner), cookies: login.Result().Cookies(), csrf: sess.CSRFToken, logs: logs}
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
// List-Unsubscribe / List-Unsubscribe-Post（DESIGN.md §4.7）。
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

// issueResetToken 走真实触发点 POST /forgot-password，返回邮件正文里的明文令牌与那封邮件。
func issueResetToken(t *testing.T, ts securityTestServer) (string, store.OutboxMessage) {
	t.Helper()
	rec := postForm(t, ts.srv, "/forgot-password", url.Values{"email": {"owner@example.com"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /forgot-password status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypePasswordReset)
	if len(rows) == 0 {
		t.Fatal("no password_reset mail enqueued after POST /forgot-password")
	}
	last := rows[len(rows)-1]
	return tokenFromBody(t, last.TextBody), last
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
	verify := postForm(t, ts.srv, "/settings/verify-email", url.Values{
		auth.CSRFFieldName: {ts.csrf},
	}, ts.cookies)
	if verify.Code != http.StatusOK {
		t.Fatalf("POST /settings/verify-email status = %d, want 200 (body %s)", verify.Code, snippet(verify.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(verifyRows) != 1 {
		t.Errorf("email_verification rows after resend = %d, want 1", len(verifyRows))
	}
	assertNoUnsubscribeHeader(t, verifyRows)

	// 4) 凭据变更通知（改密码）。
	change := postForm(t, ts.srv, "/settings/password", url.Values{
		auth.CSRFFieldName: {ts.csrf},
		"old_password":     {"Sup3rSecret!"},
		"new_password":     {"N3wSup3rSecret!"},
	}, ts.cookies)
	if change.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/password status = %d, want 303 (body %s)", change.Code, snippet(change.Body.String()))
	}
	credRows := outboxByType(t, ts.db, mail.TypeCredentialChanged)
	if len(credRows) != 1 {
		t.Errorf("credential_changed rows after a password change = %d, want 1", len(credRows))
	}
	assertNoUnsubscribeHeader(t, credRows)

	// 5) 账号被禁用通知（管理员禁用另一个账号）。
	targetID := createTargetUser(t, ts)
	disable := postForm(t, ts.srv, "/admin/users/"+strconv.FormatUint(targetID, 10)+"/status", url.Values{
		auth.CSRFFieldName: {ts.csrf},
		"action":           {"disable"},
	}, ts.cookies)
	if disable.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/users/:id/status status = %d, want 303 (body %s)", disable.Code, snippet(disable.Body.String()))
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

	// 首次：表单可达且提交成功。
	form := getWithCookies(t, ts.srv, "/reset-password?token="+url.QueryEscape(plain), nil)
	if form.Code != http.StatusOK {
		t.Fatalf("GET /reset-password?token=... status = %d, want 200 (body %s)", form.Code, snippet(form.Body.String()))
	}
	first := postForm(t, ts.srv, "/reset-password", url.Values{
		"token": {plain}, "password": {"N3wSup3rSecret!"},
	}, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first reset submit status = %d, want 200 (body %s)", first.Code, snippet(first.Body.String()))
	}
	if !strings.Contains(first.Body.String(), "密码已重置") {
		t.Errorf("first reset did not report success: %s", snippet(first.Body.String()))
	}

	// 新密码真的生效：用新密码登录应 303。
	login := postForm(t, ts.srv, "/login", url.Values{
		"username": {"owner"}, "password": {"N3wSup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Errorf("login with the reset password status = %d, want 303 (body %s)", login.Code, snippet(login.Body.String()))
	}

	// 二次使用同一令牌：必须被拒。
	second := postForm(t, ts.srv, "/reset-password", url.Values{
		"token": {plain}, "password": {"An0therSecret!"},
	}, nil)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second reset submit status = %d, want 400 (body %s)", second.Code, snippet(second.Body.String()))
	}
	if !strings.Contains(second.Body.String(), "已被使用过") {
		t.Errorf("second reset did not report the link as used: %s", snippet(second.Body.String()))
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
	rec := postForm(t, ts.srv, "/reset-password", url.Values{
		"token": {plain}, "password": {"N3wSup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired reset submit status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "已过期") {
		t.Errorf("expired reset did not report expiry: %s", snippet(rec.Body.String()))
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

	// 再走一遍消费路径，确保失败分支也不打明文。
	postForm(t, ts.srv, "/reset-password", url.Values{"token": {plain}, "password": {"N3wSup3rSecret!"}}, nil)
	postForm(t, ts.srv, "/reset-password", url.Values{"token": {plain}, "password": {"N3wSup3rSecret!"}}, nil)

	if logs := ts.logs.String(); strings.Contains(logs, plain) {
		t.Errorf("the plaintext reset token appears in the server log:\n%s", snippet(logs))
	}
}

// TestSecurityMailClassAHasNoUnsubscribeHeader 是反面用例：五类 A 类邮件都不携带退订头。
func TestSecurityMailClassAHasNoUnsubscribeHeader(t *testing.T) {
	ts := newSecurityServer(t, true)
	// 触发全部五类。
	_, _ = issueResetToken(t, ts)
	postForm(t, ts.srv, "/settings/verify-email", url.Values{auth.CSRFFieldName: {ts.csrf}}, ts.cookies)
	postForm(t, ts.srv, "/settings/password", url.Values{
		auth.CSRFFieldName: {ts.csrf}, "old_password": {"Sup3rSecret!"}, "new_password": {"N3wSup3rSecret!"},
	}, ts.cookies)
	targetID := createTargetUser(t, ts)
	postForm(t, ts.srv, "/admin/users/"+strconv.FormatUint(targetID, 10)+"/status", url.Values{
		auth.CSRFFieldName: {ts.csrf}, "action": {"disable"},
	}, ts.cookies)

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

// TestSecurityMailEmailVerifyAndChangeFlow 覆盖邮箱验证与改邮箱确认两条链路。
func TestSecurityMailEmailVerifyAndChangeFlow(t *testing.T) {
	ts := newSecurityServer(t, true)

	// 邮箱验证：重发 → 点链接 → email_verified_at 落值；令牌一次性。
	resend := postForm(t, ts.srv, "/settings/verify-email", url.Values{auth.CSRFFieldName: {ts.csrf}}, ts.cookies)
	if resend.Code != http.StatusOK {
		t.Fatalf("resend verification status = %d, want 200 (body %s)", resend.Code, snippet(resend.Body.String()))
	}
	verifyRows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	plain := tokenFromBody(t, verifyRows[len(verifyRows)-1].TextBody)
	ok := getWithCookies(t, ts.srv, "/verify-email?token="+url.QueryEscape(plain), nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("GET /verify-email status = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	if !strings.Contains(ok.Body.String(), "邮箱已验证") {
		t.Errorf("verification page did not confirm success: %s", snippet(ok.Body.String()))
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.EmailVerifiedAt == nil {
		t.Error("email_verified_at is still NULL after clicking the verification link")
	}
	again := getWithCookies(t, ts.srv, "/verify-email?token="+url.QueryEscape(plain), nil)
	if again.Code != http.StatusBadRequest || !strings.Contains(again.Body.String(), "已被使用过") {
		t.Errorf("reused verification token status = %d body = %s, want 400 and 'already used'", again.Code, snippet(again.Body.String()))
	}

	// 改邮箱：向新地址发确认信，确认前不改库。
	change := postForm(t, ts.srv, "/settings/email", url.Values{
		auth.CSRFFieldName: {ts.csrf}, "email": {"new-owner@example.com"},
	}, ts.cookies)
	if change.Code != http.StatusOK {
		t.Fatalf("POST /settings/email status = %d, want 200 (body %s)", change.Code, snippet(change.Body.String()))
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
	confirm := getWithCookies(t, ts.srv, "/confirm-email-change?token="+url.QueryEscape(changeToken), nil)
	if confirm.Code != http.StatusOK {
		t.Fatalf("GET /confirm-email-change status = %d, want 200 (body %s)", confirm.Code, snippet(confirm.Body.String()))
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
	postForm(t, ts.srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	after := len(outboxByType(t, ts.db, mail.TypeNewDeviceLogin))
	if after != before {
		t.Errorf("new_device_login rows %d -> %d after an identical second login, want no new mail", before, after)
	}
}

// TestSecurityMailDisabledWhenSMTPNotConfigured 断言 SMTP 未配置时流程禁用并说明原因，绝不静默。
func TestSecurityMailDisabledWhenSMTPNotConfigured(t *testing.T) {
	ts := newSecurityServer(t, false)
	page := getWithCookies(t, ts.srv, "/forgot-password", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /forgot-password status = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), "本站未开启邮件功能") {
		t.Errorf("forgot-password page does not explain that mail is disabled: %s", snippet(page.Body.String()))
	}
	rec := postForm(t, ts.srv, "/forgot-password", url.Values{"email": {"owner@example.com"}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /forgot-password status = %d, want 303", rec.Code)
	}
	if rows := outboxByType(t, ts.db, mail.TypePasswordReset); len(rows) != 0 {
		t.Errorf("password_reset mail enqueued while SMTP is unconfigured: %d rows", len(rows))
	}
}
