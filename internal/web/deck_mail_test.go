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
// 的验收（DESIGN.md §4.7）。都走真实触发点：共享走 HTTP 端点，优化完成走作业成功钩子的入口。

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

// TestDeckGrantMailNotifiesTheGrantee 覆盖分享→改造→撤销三步的通知类型与收件人。
func TestDeckGrantMailNotifiesTheGrantee(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Shared deck")
	sender := startInviteMail(t, srv, db)
	grantee := newGrantee(t, srv, "grantee", "grantee@example.com")
	base := "/api/v1/decks/" + u64str(deck.ID) + "/sharing/grants"

	// ① 分享：给被授权者发 deck_shared，且写清卡组名与分享人。
	rec := jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":`+u64str(grantee.ID)+`,"role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs := waitForMail(t, sender, 1)
	first := msgs[0]
	if first.Type != string(mail.TypeDeckShared) {
		t.Errorf("mail type = %q, want %q", first.Type, mail.TypeDeckShared)
	}
	if first.To != grantee.Email {
		t.Errorf("mail to = %q, want the grantee %q", first.To, grantee.Email)
	}
	if !strings.Contains(first.TextBody, "Shared deck") {
		t.Errorf("text body = %q, want the deck name", first.TextBody)
	}
	// B 类可退订：既要 RFC 8058 头，正文里也要有入口。
	if _, ok := first.Headers["List-Unsubscribe"]; !ok {
		t.Errorf("headers = %v, want List-Unsubscribe for a class B mail", first.Headers)
	}

	// ② 改角色：deck_permission_changed，而不是再发一次「分享给你」。
	rec = jsonRequest(t, srv, http.MethodPatch, base+"/"+u64str(grantee.ID),
		`{"role":"editor"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("PATCH grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs = waitForMail(t, sender, 2)
	if msgs[1].Type != string(mail.TypeDeckPermissionChanged) {
		t.Errorf("second mail type = %q, want %q", msgs[1].Type, mail.TypeDeckPermissionChanged)
	}

	// ③ 撤销：仍是 permission_changed，正文要说「被取消」而不是「改成了空」。
	rec = jsonRequest(t, srv, http.MethodDelete, base+"/"+u64str(grantee.ID), "", cookies, csrf)
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

	rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/decks/"+u64str(deck.ID)+"/sharing/grants",
		`{"user_id":`+u64str(grantee.ID)+`,"role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success", rec.Code)
	}
	time.Sleep(150 * time.Millisecond)
	if got := len(sender.messages()); got != 0 {
		t.Errorf("delivered %d messages, want 0 (recipient opted out)", got)
	}
	// 共享本身必须照常生效：通知只是副作用。
	if role, err := store.NewGrantStore(db).Role(context.Background(), deck.ID, grantee.ID); err != nil || role == "" {
		t.Errorf("grant role = %q (err %v), want the share to be applied", role, err)
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
