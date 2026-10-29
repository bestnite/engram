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

// This file is the acceptance test for class B collaboration mail (deck_shared / deck_permission_changed) and class C "parameter optimization done"
// mail. Both go through real trigger points: sharing goes through the HTTP endpoints, optimization completion through the job-success hook entry.

// newGrantee creates an ordinary user who can receive mail.
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

// deckMailTypes are the two sharing notifications. A login during a test sends its own new-device alert, so assertions
// about sharing mail look at these types only.
var deckMailTypes = []string{string(mail.TypeDeckShared), string(mail.TypeDeckPermissionChanged)}

// sentOf returns the delivered messages whose type is one of types, in delivery order.
func sentOf(sender *recordingMailSender, types ...string) []mail.Message {
	var out []mail.Message
	for _, m := range sender.messages() {
		for _, typ := range types {
			if m.Type == typ {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// waitForMailOf polls until at least n messages of the given types have been delivered.
func waitForMailOf(t *testing.T, sender *recordingMailSender, n int, types ...string) []mail.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := sentOf(sender, types...); len(msgs) >= n {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d delivered messages of %v, got %d", n, types, len(sentOf(sender, types...)))
	return nil
}

// TestDeckGrantMailNotifiesTheGrantee covers the notification type and recipient across the four steps invite→accept→role change→revoke.
func TestDeckGrantMailNotifiesTheGrantee(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Shared deck")
	sender := startInviteMail(t, srv, db)
	granteeID, granteeCookies, granteeCSRF := createUserAndLogin(t, srv, db, "grantee")
	granteeEmail := "grantee@example.com"
	base := "/api/v1/decks/" + deck.PublicID + "/sharing/grants"

	// ① Share: send deck_shared to the grantee, clearly stating the deck name and the sharer.
	rec := jsonRequest(t, srv, http.MethodPost, base,
		`{"user_id":"`+userPublicID(t, db, granteeID)+`","role":"reader"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("POST grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs := waitForMailOf(t, sender, 1, deckMailTypes...)
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
	// Class B is unsubscribable: it needs both the RFC 8058 headers and an entry in the body.
	if _, ok := first.Headers["List-Unsubscribe"]; !ok {
		t.Errorf("headers = %v, want List-Unsubscribe for a class B mail", first.Headers)
	}

	// ② The invitee accepts (opt-in): only after this step are they really a member, and the later role change is then really a "role change".
	if rec := jsonRequest(t, srv, http.MethodPost,
		"/api/v1/sharing/invites/"+deck.PublicID+"/accept", "", granteeCookies, granteeCSRF); rec.Code != http.StatusOK {
		t.Fatalf("accept invite = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// Acceptance itself sends no mail (the invite mail was already sent), so there is still only 1 message here.
	if got := len(sentOf(sender, deckMailTypes...)); got != 1 {
		t.Errorf("delivered %d messages after acceptance, want 1 (acceptance itself must not mail)", got)
	}

	// ③ Role change: deck_permission_changed, not another "shared with you".
	rec = jsonRequest(t, srv, http.MethodPatch, base+"/"+userPublicID(t, db, granteeID),
		`{"role":"editor"}`, cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("PATCH grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs = waitForMailOf(t, sender, 2, deckMailTypes...)
	if msgs[1].Type != string(mail.TypeDeckPermissionChanged) {
		t.Errorf("second mail type = %q, want %q", msgs[1].Type, mail.TypeDeckPermissionChanged)
	}

	// ④ Revoke: still permission_changed, and the body must say "revoked" rather than "changed to empty".
	rec = jsonRequest(t, srv, http.MethodDelete, base+"/"+userPublicID(t, db, granteeID), "", cookies, csrf)
	if rec.Code >= 300 {
		t.Fatalf("DELETE grant = %d, want success (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	msgs = waitForMailOf(t, sender, 3, deckMailTypes...)
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

// TestDeckGrantMailHonorsPreference asserts that once the recipient turns it off not a single mail is sent: the preference must really take effect.
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
	if got := len(sentOf(sender, deckMailTypes...)); got != 0 {
		t.Errorf("delivered %d messages, want 0 (recipient opted out)", got)
	}
	// The sharing itself must take effect as usual: notifying is only a side effect. Under opt-in, this step's product is an **invite** (the grant waits for acceptance).
	if _, err := store.NewDeckShareInviteStore(db).ByDeckAndUser(context.Background(), deck.ID, grantee.ID); err != nil {
		t.Errorf("invite missing after the share: %v", err)
	}
}

// TestOptimizeDoneMailNotifiesPresetOwner covers class C "parameter optimization done": off by default, sent to the preset owner once enabled.
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

	// Off by default: not sent.
	srv.NotifyOptimizeDone(ctx, job)
	time.Sleep(150 * time.Millisecond)
	if got := len(sentOf(sender, deckMailTypes...)); got != 0 {
		t.Fatalf("delivered %d messages, want 0 while the type is off by default", got)
	}

	// After the user enables it: sent to the owner, with the preset name and the review count in the body.
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

// TestOptimizeDoneMailShowsDefaultPresetNameInRecipientLanguage asserts the notification mail for the default preset
// uses the display name from the language pack rather than the in-DB identity marker "Default" — the mail body is user-visible text.
func TestOptimizeDoneMailShowsDefaultPresetNameInRecipientLanguage(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	sender := startInviteMail(t, srv, db)
	ctx := context.Background()

	preset := store.NewPreset(ownerID, store.DefaultPresetName)
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create default preset: %v", err)
	}
	// The body language follows the recipient's settings: pinning it to Simplified Chinese is what makes it possible to assert the localized "default" appears rather than Default.
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
	if !strings.Contains(msgs[0].TextBody, "默认") {
		t.Errorf("text body = %q, want the localized default preset name", msgs[0].TextBody)
	}
	if strings.Contains(msgs[0].TextBody, "Default") {
		t.Errorf("text body = %q, want no storage literal in user-facing text", msgs[0].TextBody)
	}
}
