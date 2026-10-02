package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/auth"
	"example.com/engram/internal/jobs"
	"example.com/engram/internal/mail"
	"example.com/engram/internal/store"
)

// 本文件是 M1-24（D 类管理员通知邮件）的验收测试。
//
// 覆盖：三个在本仓库真实存在的触发点各自把正确类型的邮件送到**管理员登录邮箱**；
// 「邮件失败不影响触发操作」；D 类可关闭；SMTP 未配置时页面渲染 mail.not_configured。
//
// 「备份失败」触发点在本仓库没有备份代码，故不在此测试（报告里已说明）。

// attachAdminMail 写入生效的 SMTP 设置并给测试服务挂一个 outbox；worker 不启动，
// 因此入队邮件留在 outbox 表里，测试直接按类型查询即可（与 A 类测试同法）。
func attachAdminMail(t *testing.T, srv *Server, db *gorm.DB) {
	t.Helper()
	configureSMTP(t, db)
	srv.mail = mail.NewOutbox(mail.Deps{DB: db, Logger: discardLogger()})
}

// waitOutboxType 轮询等待 outbox 里出现指定类型的邮件（作业失败钩子在 worker 里异步触发）。
func waitOutboxType(t *testing.T, db *gorm.DB, typ mail.Type) []store.OutboxMessage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rows := outboxByType(t, db, typ); len(rows) > 0 {
			return rows
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %s mail enqueued within the timeout", typ)
	return nil
}

// openRegistration 把注册策略设为 open，让无邀请的自助注册可放行。
func openRegistration(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := store.PutSetting(context.Background(), db, auth.SettingKeyRegistrationPolicy,
		auth.PolicyOpen, nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(registration_policy): %v", err)
	}
}

// TestAdminMailRegistrationReachesAdmin 触发点一：注册成功后，管理员收到 registration_pending。
func TestAdminMailRegistrationReachesAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	attachAdminMail(t, srv, db)
	openRegistration(t, db)

	rec := postForm(t, srv, "/register", url.Values{
		"username": {"newbie"}, "email": {"newbie@example.com"}, "password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /register = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	rows := outboxByType(t, db, mail.TypeRegistrationPending)
	if len(rows) != 1 {
		t.Fatalf("registration_pending rows = %d, want 1", len(rows))
	}
	if rows[0].To != "owner@example.com" {
		t.Errorf("registration_pending recipient = %q, want the admin login email owner@example.com", rows[0].To)
	}
	t.Logf("registration triggered %s mail to admin %s (subject %q)", rows[0].Type, rows[0].To, rows[0].Subject)
}

// TestAdminMailJobFailureReachesAdmin 触发点二：作业失败后，管理员收到 job_failed。
// 这里跑真实 Runner（/bin/sh -c 'exit 3'）并把它接到 web 层的 NotifyJobFailed，
// 因此覆盖了 jobs.OnFailure 钩子与投递的完整链路。
func TestAdminMailJobFailureReachesAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	attachAdminMail(t, srv, db)

	runner, err := jobs.New(jobs.Deps{
		DB: db, Logger: discardLogger(),
		Command: func(context.Context, *store.Job, jobs.Reporter) (jobs.Command, error) {
			return jobs.Command{Name: "/bin/sh", Args: []string{"-c", "exit 3"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	runner.SetOnFailure(srv.NotifyJobFailed)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)

	if _, err := runner.Enqueue(ctx, jobs.KindOptimize, nil); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rows := waitOutboxType(t, db, mail.TypeJobFailed)
	if rows[0].To != "owner@example.com" {
		t.Errorf("job_failed recipient = %q, want owner@example.com", rows[0].To)
	}
	t.Logf("failed job triggered %s mail to admin %s (subject %q)", rows[0].Type, rows[0].To, rows[0].Subject)
}

// TestAdminMailMediaQuotaReachesAdmin 触发点三：用户上传被媒体配额拦下时，管理员收到 media_disk_alert。
func TestAdminMailMediaQuotaReachesAdmin(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	attachAdminMail(t, srv, db)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(media_user_quota_bytes): %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	// 先占满 1500 字节：上传 + 让 note 引用它。
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.ID)

	// 再传 1000：1500+1000 > 2048 → 413，并触发管理员告警。
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota upload = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}

	rows := outboxByType(t, db, mail.TypeMediaDiskAlert)
	if len(rows) != 1 {
		t.Fatalf("media_disk_alert rows = %d, want 1", len(rows))
	}
	if rows[0].To != "owner@example.com" {
		t.Errorf("media_disk_alert recipient = %q, want owner@example.com", rows[0].To)
	}
	t.Logf("quota rejection triggered %s mail to admin %s (subject %q)", rows[0].Type, rows[0].To, rows[0].Subject)
}

// TestAdminMailHonorsAdminPreference 证明 D 类可关：收件管理员关掉某类型后不再收到该类邮件，
// 但触发操作照常成功。
func TestAdminMailHonorsAdminPreference(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	attachAdminMail(t, srv, db)
	openRegistration(t, db)
	if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), ownerID,
		map[string]bool{string(mail.TypeRegistrationPending): false}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	rec := postForm(t, srv, "/register", url.Values{
		"username": {"quiet"}, "email": {"quiet@example.com"}, "password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /register = %d, want 303", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if rows := outboxByType(t, db, mail.TypeRegistrationPending); len(rows) != 0 {
		t.Errorf("registration_pending rows = %d, want 0 (admin opted out)", len(rows))
	}
}

// brokenMailOutbox 构造一个「报告已配置但入队必然失败」的 outbox：环境变量提供 host/from
// 让 Configured() 为真，而 DB 为空让 Enqueue 返回错误。用来确定性地验证「发信失败不影响触发操作」。
func brokenMailOutbox(t *testing.T) *mail.Outbox {
	t.Helper()
	t.Setenv(mail.EnvSMTPHost, "smtp.test")
	t.Setenv(mail.EnvSMTPFrom, "noreply@test")
	ob := mail.NewOutbox(mail.Deps{DB: nil, Logger: discardLogger()})
	if !ob.Configured() {
		t.Fatal("fixture outbox should report configured; the enqueue path would not be exercised")
	}
	return ob
}

// TestAdminMailFailureDoesNotBreakTrigger 是核心反面用例：入队失败时，注册与媒体配额拦截
// 这两个触发操作仍然成功（一个 303、一个 413），绝不因邮件而失败。
func TestAdminMailFailureDoesNotBreakTrigger(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	srv.mail = brokenMailOutbox(t)

	// 注册：入队会失败，但账号必须创建成功。
	openRegistration(t, db)
	rec := postForm(t, srv, "/register", url.Values{
		"username": {"resilient"}, "email": {"resilient@example.com"}, "password": {"Sup3rSecret!"},
	}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("registration with a broken mailer = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var u store.User
	if err := db.Where("username = ?", "resilient").First(&u).Error; err != nil {
		t.Fatalf("registration did not create the user despite mail failure: %v", err)
	}

	// 媒体配额：入队会失败，但上传仍以 413 被拒。
	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(context.Background(), db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting(quota): %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.ID)
	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota upload with a broken mailer = %d, want 413", rec.Code)
	}
	t.Logf("mail enqueue failed on every trigger, yet registration returned 303 and quota rejection returned 413")
}

// TestAdminMailUnconfiguredExplainedOnSMTPPage 证明 SMTP 未配置时页面渲染 mail.not_configured，
// 绝不静默。
func TestAdminMailUnconfiguredExplainedOnSMTPPage(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	// 不调用 attachAdminMail：srv.mail 保持为空（未配置）。

	page := getWithCookies(t, srv, "/admin/smtp", cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /admin/smtp = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	if !strings.Contains(page.Body.String(), "本站未开启邮件功能") {
		t.Errorf("SMTP page does not render mail.not_configured for class-D notifications; body = %s", snippet(page.Body.String()))
	}
}
