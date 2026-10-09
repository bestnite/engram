package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 B 类协作邮件（deck_shared / deck_permission_changed）与 C 类「参数优化完成」
// 的验收。都走真实触发点：共享走 HTTP 端点，优化完成走作业成功钩子的入口。

// newGrantee 建一个可收信的普通用户。
func newGrantee(t *testing.T, srv *Server, name, email string) *store.User {
	t.Helper()
	u, err := srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: name, Email: email, Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	return u
}

// TestDeckGrantMailNotifiesTheGrantee 覆盖邀请→接受→改造→撤销四步的通知类型与收件人。
func TestDeckGrantMailNotifiesTheGrantee(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Shared deck")
	sender := startInviteMail(t, srv, db)
	granteeID, granteeCookies, granteeCSRF := createUserAndLogin(t, srv, db, "grantee")
	granteeEmail := "grantee@example.com"
	base := "/api/v1/decks/" + deck.PublicID + "/sharing/grants"

	// ① 分享：给被授权者发 deck_shared，且写清卡组名与分享人。
	rec := jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":"`+userPublicID(t, db, granteeID)+`","role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs := waitForMail(t, sender, 1)
	first := msgs[0]
	if first.Type != string(mail.TypeDeckShared) {
		t.Errorf("mail type = %q, want %q", first.Type, mail.TypeDeckShared)
	}
	if first.To != granteeEmail {
		t.Errorf("mail to = %q, want the grantee %q", first.To, granteeEmail)
	}
	if !strings.Contains(first.TextBody, "Shared deck") {
		t.Errorf("text body = %q, want the deck name", first.TextBody)
	}
	// B 类可退订：既要 RFC 8058 头，正文里也要有入口。
	if _, ok := first.Headers["List-Unsubscribe"]; !ok {
		t.Errorf("headers = %v, want List-Unsubscribe for a class B mail", first.Headers)
	}

	// ② 被邀请者接受（同意制）：这一步之后他才是真的成员，后面的改角色才是「改角色」。
	if rec := jsonRequest(t, srv, http.MethodPost,
		"/api/v1/sharing/invites/"+deck.PublicID+"/accept", "", granteeCookies, granteeCSRF); rec.Code != http.StatusOK {
		t.Fatalf("accept invite = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 接受本身不发信（邀请信已经发过了），所以这里仍只有 1 封。
	if got := len(sender.messages()); got != 1 {
		t.Errorf("delivered %d messages after acceptance, want 1 (acceptance itself must not mail)", got)
	}

	// ③ 改角色：deck_permission_changed，而不是再发一次「分享给你」。
	rec = jsonRequest(t, srv, http.MethodPatch, base+"/"+userPublicID(t, db, granteeID),
		`{"role":"editor"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("PATCH grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs = waitForMail(t, sender, 2)
	if msgs[1].Type != string(mail.TypeDeckPermissionChanged) {
		t.Errorf("second mail type = %q, want %q", msgs[1].Type, mail.TypeDeckPermissionChanged)
	}

	// ④ 撤销：仍是 permission_changed，正文要说「被取消」而不是「改成了空」。
	rec = jsonRequest(t, srv, http.MethodDelete, base+"/"+userPublicID(t, db, granteeID), "", cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("DELETE grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs = waitForMail(t, sender, 3)
	if msgs[2].Type != string(mail.TypeDeckPermissionChanged) {
		t.Errorf("third mail type = %q, want %q", msgs[2].Type, mail.TypeDeckPermissionChanged)
	}
	if !strings.Contains(msgs[2].TextBody, "Shared deck") {
		t.Errorf("revoke body = %q, want the deck name", msgs[2].TextBody)
	}
	if strings.Contains(msgs[2].TextBody, "{{") || strings.Contains(msgs[2].TextBody, "%!") {
		t.Errorf("revoke body = %q, want no unrendered placeholder", msgs[2].TextBody)
	}
}

// TestDeckGrantMailHonorsPreference 断言收件人关掉之后一封信都不发：偏好必须真的生效。
func TestDeckGrantMailHonorsPreference(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Quiet deck")
	sender := startInviteMail(t, srv, db)
	grantee := newGrantee(t, srv, "quiet", "quiet@example.com")
	if err := store.NewEmailPrefStore(db).SetChoices(context.Background(), grantee.ID,
		map[string]bool{string(mail.TypeDeckShared): false}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/sharing/grants",
		`{"user_id":"`+grantee.PublicID+`","role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success", rec.Code)
	}
	time.Sleep(150 * time.Millisecond)
	if got := len(sender.messages()); got != 0 {
		t.Errorf("delivered %d messages, want 0 (recipient opted out)", got)
	}
	// 分享本身必须照常生效：通知只是副作用。同意制下这一步的产物是**邀请**（授权等对方接受）。
	if _, err := store.NewDeckShareInviteStore(db).ByDeckAndUser(context.Background(), deck.ID, grantee.ID); err != nil {
		t.Errorf("invite missing after the share: %v", err)
	}
}

// TestOptimizeDoneMailNotifiesPresetOwner 覆盖 C 类「参数优化完成」：默认关，开启后发给预设属主。
func TestOptimizeDoneMailNotifiesPresetOwner(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	sender := startInviteMail(t, srv, db)
	ctx := context.Background()

	preset := store.NewPreset(ownerID, "Speed preset")
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	owner, err := store.NewUserStore(db).ByID(ctx, ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	result := `{"reviews_used":321,"weights":[],"fit_before":{"log_loss":0,"rmse":0,"items":0},"fit_after":{"log_loss":0,"rmse":0,"items":0},"optimized_at":"2026-01-01T00:00:00Z"}`
	job := store.Job{Kind: jobs.KindOptimize, TargetID: &preset.ID, Status: jobs.StatusSucceeded,
		ResultJSON: &result, CreatedAt: time.Now().UTC()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	// 默认关：不发。
	srv.NotifyOptimizeDone(ctx, job)
	time.Sleep(150 * time.Millisecond)
	if got := len(sender.messages()); got != 0 {
		t.Fatalf("delivered %d messages, want 0 while the type is off by default", got)
	}

	// 用户开启后：发给属主，正文带预设名与复习条数。
	if err := store.NewEmailPrefStore(db).SetChoices(ctx, ownerID,
		map[string]bool{string(mail.TypeOptimizeDone): true}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}
	srv.NotifyOptimizeDone(ctx, job)
	msgs := waitForMail(t, sender, 1)
	if msgs[0].Type != string(mail.TypeOptimizeDone) {
		t.Errorf("mail type = %q, want %q", msgs[0].Type, mail.TypeOptimizeDone)
	}
	if msgs[0].To != owner.Email {
		t.Errorf("mail to = %q, want the preset owner %q", msgs[0].To, owner.Email)
	}
	if !strings.Contains(msgs[0].TextBody, "Speed preset") || !strings.Contains(msgs[0].TextBody, "321") {
		t.Errorf("text body = %q, want the preset name and the review count", msgs[0].TextBody)
	}
}

// TestOptimizeDoneMailShowsDefaultPresetNameInRecipientLanguage 断言默认预设的通知邮件
// 用语言包里的显示名，而不是库内的身份标识 "Default"——邮件正文是用户可见文本。
func TestOptimizeDoneMailShowsDefaultPresetNameInRecipientLanguage(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	sender := startInviteMail(t, srv, db)
	ctx := context.Background()

	preset := store.NewPreset(ownerID, store.DefaultPresetName)
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create default preset: %v", err)
	}
	// 正文语言由收件人设置决定：钉成简体中文，才能断言出现的是「默认」而不是 Default。
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("locale", "zh-CN").Error; err != nil {
		t.Fatalf("set owner locale: %v", err)
	}
	if err := store.NewEmailPrefStore(db).SetChoices(ctx, ownerID,
		map[string]bool{string(mail.TypeOptimizeDone): true}, time.Now().UTC()); err != nil {
		t.Fatalf("SetChoices: %v", err)
	}

	result := `{"reviews_used":42,"weights":[],"fit_before":{"log_loss":0,"rmse":0,"items":0},"fit_after":{"log_loss":0,"rmse":0,"items":0},"optimized_at":"2026-01-01T00:00:00Z"}`
	job := store.Job{Kind: jobs.KindOptimize, TargetID: &preset.ID, Status: jobs.StatusSucceeded,
		ResultJSON: &result, CreatedAt: time.Now().UTC()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}

	srv.NotifyOptimizeDone(ctx, job)
	msgs := waitForMail(t, sender, 1)
	if !strings.Contains(msgs[0].TextBody, "预设「默认」") {
		t.Errorf("text body = %q, want the localized default preset name", msgs[0].TextBody)
	}
	if strings.Contains(msgs[0].TextBody, "预设「Default」") {
		t.Errorf("text body = %q, want no storage literal in user-facing text", msgs[0].TextBody)
	}
}
