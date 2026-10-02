package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
)

// 本文件是 M1-20 的 HTTP 级验收：邀请码可由管理员直接寄到被邀请人邮箱（B 类）。
//
// 覆盖四条验收线：
//  1. 为某邮箱建的邀请能投递并被接受；
//  2. 被撤销 / 过期的邀请链接被拒；
//  3. 手工拷链接的原有路径仍然能用；
//  4. SMTP 未配置时入口渲染 mail.not_configured 说明，且不静默。
//
// 投递用进程内替身（recordingMailSender）完成，不依赖外网或真实 SMTP。

// recordingMailSender 是 mail.Sender 的替身：只记录被投递的邮件，不碰网络。
type recordingMailSender struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (r *recordingMailSender) Send(ctx context.Context, cfg mail.SMTPConfig, m mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

func (r *recordingMailSender) Test(ctx context.Context, cfg mail.SMTPConfig) error { return nil }

func (r *recordingMailSender) messages() []mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]mail.Message(nil), r.sent...)
}

// configureSMTP 写入生效的 SMTP 设置；host 与 from 齐备即视为已配置。
func configureSMTP(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	for k, v := range map[string]string{
		mail.SettingKeySMTPHost: "127.0.0.1",
		mail.SettingKeySMTPPort: "2525",
		mail.SettingKeySMTPFrom: "no-reply@example.com",
	} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
}

// startInviteMail 构造并启动一个已配置 SMTP 的 outbox，挂到测试服务上；返回替身。
func startInviteMail(t *testing.T, srv *Server, db *gorm.DB) *recordingMailSender {
	t.Helper()
	configureSMTP(t, db)
	sender := &recordingMailSender{}
	ob := mail.NewOutbox(mail.Deps{
		DB: db, Sender: sender,
		Retry:        mail.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		PollInterval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	ob.Start(ctx)
	t.Cleanup(func() {
		ob.Stop()
		cancel()
	})
	srv.mail = ob
	return sender
}

// waitForMail 轮询等待替身收到至少 n 封邮件（worker 异步投递）。
func waitForMail(t *testing.T, sender *recordingMailSender, n int) []mail.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := sender.messages(); len(msgs) >= n {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d delivered messages, got %d", n, len(sender.messages()))
	return nil
}

// noticeFrom 从 303 重定向的 Location 里取出 notice 码。
func noticeFrom(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	return u.Query().Get("notice")
}

// TestInviteEmailDeliveredAndAccepted 是 M1-20 的核心验收：
// 为某邮箱建的邀请被投递（邮件里带可用的接受链接），并且该链接能被接受。
func TestInviteEmailDeliveredAndAccepted(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	sender := startInviteMail(t, srv, db)

	created := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"}, "expires_days": {"7"},
		"email": {"invitee@example.com"}, "send_email": {"1"},
	}, cookies)
	if created.Code != http.StatusSeeOther || noticeFrom(t, created) != noticeInviteMailQueued {
		t.Fatalf("POST /admin/invites = %d notice %q, want 303 %q",
			created.Code, noticeFrom(t, created), noticeInviteMailQueued)
	}

	invites, err := srv.invites.List(context.Background())
	if err != nil || len(invites) != 1 {
		t.Fatalf("List() = %d invites err %v, want 1", len(invites), err)
	}
	token := invites[0].Token

	msgs := waitForMail(t, sender, 1)
	msg := msgs[0]
	if msg.To != "invitee@example.com" {
		t.Errorf("delivered To = %q, want invitee@example.com", msg.To)
	}
	if msg.Type != string(mail.TypeInvite) {
		t.Errorf("delivered Type = %q, want %q", msg.Type, mail.TypeInvite)
	}
	if len(msg.Headers) != 0 {
		t.Errorf("Headers = %v, want empty (unsubscribe is M1-22)", msg.Headers)
	}
	if !strings.Contains(msg.TextBody, token) {
		t.Errorf("delivered body does not carry the invite token:\n%s", msg.TextBody)
	}

	// 用邮件里的链接接受邀请：放行一次。
	reg := postForm(t, srv, "/register", url.Values{
		"username": {"invitee"}, "email": {"invitee@example.com"},
		"password": {"Sup3rSecret!"}, "invite": {token},
	}, nil)
	if reg.Code != http.StatusSeeOther {
		t.Fatalf("register with mailed invite = %d, want 303 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
	after, _ := srv.invites.ByToken(context.Background(), token)
	if after.UsedAt == nil {
		t.Error("invite was not marked used after acceptance")
	}
}

// TestInviteLinkCopiedManuallyStillWorks 证明加邮件没有破坏原有手工拷链接路径。
func TestInviteLinkCopiedManuallyStillWorks(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	startInviteMail(t, srv, db)

	created := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"},
	}, cookies)
	if created.Code != http.StatusSeeOther || noticeFrom(t, created) != "invite_created" {
		t.Fatalf("POST /admin/invites = %d notice %q, want 303 invite_created",
			created.Code, noticeFrom(t, created))
	}
	invites, _ := srv.invites.List(context.Background())
	if len(invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(invites))
	}
	reg := postForm(t, srv, "/register", url.Values{
		"username": {"manual"}, "email": {"manual@example.com"},
		"password": {"Sup3rSecret!"}, "invite": {invites[0].Token},
	}, nil)
	if reg.Code != http.StatusSeeOther {
		t.Fatalf("register with copied invite = %d, want 303 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
}

// TestRevokedAndExpiredInviteRefused 覆盖反面用例：被撤销 / 过期的链接被拒。
func TestRevokedAndExpiredInviteRefused(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)

	// 撤销：创建 → 撤销 → 用原 token 注册被拒。
	if rec := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"},
	}, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("create invite = %d, want 303", rec.Code)
	}
	invites, _ := srv.invites.List(context.Background())
	if len(invites) != 1 {
		t.Fatalf("invites = %d, want 1", len(invites))
	}
	revoked := invites[0]
	rev := postForm(t, srv, "/admin/invites/"+u64str(revoked.ID)+"/revoke",
		url.Values{"csrf_token": {csrf}}, cookies)
	if rev.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d, want 303", rev.Code)
	}
	denied := postForm(t, srv, "/register", url.Values{
		"username": {"late"}, "email": {"late@example.com"},
		"password": {"Sup3rSecret!"}, "invite": {revoked.Token},
	}, nil)
	if denied.Code != http.StatusForbidden {
		t.Errorf("register with revoked invite = %d, want 403", denied.Code)
	}

	// 过期：直接写入一条已过期的邀请 → 注册被拒。
	expired := &store.Invite{Role: store.RoleUser}
	exp := time.Now().UTC().Add(-time.Hour)
	expired.ExpiresAt = &exp
	if err := srv.invites.Create(context.Background(), expired); err != nil {
		t.Fatalf("create expired invite: %v", err)
	}
	deniedExp := postForm(t, srv, "/register", url.Values{
		"username": {"stale"}, "email": {"stale@example.com"},
		"password": {"Sup3rSecret!"}, "invite": {expired.Token},
	}, nil)
	if deniedExp.Code != http.StatusForbidden {
		t.Errorf("register with expired invite = %d, want 403", deniedExp.Code)
	}
}

// TestInviteMailEntryUnconfiguredExplains 覆盖 SMTP 未配置：入口渲染说明、提交也得到说明，
// 且邀请仍然创建成功（绝不静默，也不因邮件未配置而阻断创建）。
func TestInviteMailEntryUnconfiguredExplains(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	srv.mail = nil // 未装配邮件

	page := getWithCookies(t, srv, "/admin/registration", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /admin/registration = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), "本站未开启邮件功能") {
		t.Errorf("invite page does not explain the unconfigured mail state; body = %s", snippet(page.Body.String()))
	}

	created := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"},
		"email": {"invitee@example.com"}, "send_email": {"1"},
	}, cookies)
	if created.Code != http.StatusSeeOther || noticeFrom(t, created) != noticeInviteMailUnconfigured {
		t.Fatalf("POST with send_email while unconfigured = %d notice %q, want 303 %q",
			created.Code, noticeFrom(t, created), noticeInviteMailUnconfigured)
	}
	// 回显页面上有 mail.not_configured 的说明文案。
	after := getWithCookies(t, srv, "/admin/registration?notice="+noticeInviteMailUnconfigured, cookies)
	if !strings.Contains(after.Body.String(), "本站未开启邮件功能") {
		t.Errorf("notice page lacks the mail.not_configured explanation; body = %s", snippet(after.Body.String()))
	}
	// 邀请本身仍然创建成功。
	if n, _ := srv.invites.List(context.Background()); len(n) != 1 {
		t.Errorf("invites = %d, want 1 (invite must still be created)", len(n))
	}
}

// TestInviteMailHonorsRecipientPreference 证明 B 类可关：收件人若是本站用户并关掉了该类型，
// 就不寄信，但邀请仍然创建成功、链接仍可手拷。
func TestInviteMailHonorsRecipientPreference(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.invites = store.NewInviteStore(db)
	sender := startInviteMail(t, srv, db)

	invitee, err := srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "pref-user", Email: "pref-user@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), invitee.ID,
		map[string]bool{string(mail.TypeInvite): false}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	created := postForm(t, srv, "/admin/invites", url.Values{
		"csrf_token": {csrf}, "role": {"user"},
		"email": {"pref-user@example.com"}, "send_email": {"1"},
	}, cookies)
	if created.Code != http.StatusSeeOther || noticeFrom(t, created) != noticeInviteMailOptedOut {
		t.Fatalf("POST opted-out recipient = %d notice %q, want 303 %q",
			created.Code, noticeFrom(t, created), noticeInviteMailOptedOut)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(sender.messages()); got != 0 {
		t.Errorf("delivered %d messages, want 0 (recipient opted out)", got)
	}
	if n, _ := srv.invites.List(context.Background()); len(n) != 1 {
		t.Errorf("invites = %d, want 1 (invite must still be created)", len(n))
	}
}
