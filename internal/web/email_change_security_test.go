package web

import (
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// TestEmailChangeRequiresPasswordAndRevokesSessions pins the credential rules of the email-change flow:
// a session that cannot prove the current password must not move the mailbox, and once the change is
// confirmed every old session is dead and the replaced address is told about it.
func TestEmailChangeRequiresPasswordAndRevokesSessions(t *testing.T) {
	ts := newSecurityServer(t, true)

	// A second live session, so the "revoke the other sessions" part has something to revoke.
	otherCookies, _ := loginJSON(t, ts.srv, ts.db, "owner", "Sup3rSecret!")

	before := len(outboxByType(t, ts.db, mail.TypeEmailVerification))

	// A valid new address and no password: refused before any confirmation mail is queued.
	missing := postJSONWithSession(ts.srv, "/api/v1/settings/email",
		map[string]string{"email": "attacker@example.com"}, ts.cookies, ts.csrf)
	if missing.Code != http.StatusBadRequest || apiErrorCode(t, missing) != "password_required" {
		t.Errorf("email change without a password = %d %s, want 400 password_required",
			missing.Code, apiErrorCode(t, missing))
	}

	// A wrong password: refused with the same stable code the password-change path uses.
	wrong := postJSONWithSession(ts.srv, "/api/v1/settings/email",
		map[string]string{"email": "attacker@example.com", "password": "not-the-password"}, ts.cookies, ts.csrf)
	if wrong.Code != http.StatusUnauthorized || apiErrorCode(t, wrong) != "invalid_current_password" {
		t.Errorf("email change with a wrong password = %d %s, want 401 invalid_current_password",
			wrong.Code, apiErrorCode(t, wrong))
	}

	if got := len(outboxByType(t, ts.db, mail.TypeEmailVerification)); got != before {
		t.Errorf("refused requests queued %d email_verification mail(s), want none", got-before)
	}
	var owner store.User
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "owner@example.com" {
		t.Fatalf("email after refused changes = %q, want owner@example.com", owner.Email)
	}

	// With the current password the request goes through and the confirmation goes to the new address.
	ok := postJSONWithSession(ts.srv, "/api/v1/settings/email",
		map[string]string{"email": "new-owner@example.com", "password": "Sup3rSecret!"}, ts.cookies, ts.csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("email change with the current password = %d, want 200 (body %s)",
			ok.Code, snippet(ok.Body.String()))
	}
	rows := outboxByType(t, ts.db, mail.TypeEmailVerification)
	if len(rows) != before+1 || rows[len(rows)-1].To != "new-owner@example.com" {
		t.Fatalf("confirmation not enqueued to the new address: %+v", rows)
	}

	// Confirmation changes the address, kills both live sessions and notices the replaced mailbox.
	changeToken := tokenFromBody(t, rows[len(rows)-1].TextBody)
	cc, ch := preSessionPair(t, ts.srv, "/confirm-email-change")
	confirm := postJSON(ts.srv, "/api/v1/auth/confirm-email-change",
		map[string]string{"token": changeToken}, []*http.Cookie{cc}, ch)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm-email-change status = %d, want 200 (body %s)",
			confirm.Code, snippet(confirm.Body.String()))
	}
	if err := ts.db.First(&owner, ts.ownerID).Error; err != nil {
		t.Fatalf("reload owner: %v", err)
	}
	if owner.Email != "new-owner@example.com" {
		t.Errorf("email after confirmation = %q, want new-owner@example.com", owner.Email)
	}
	var active int64
	if err := ts.db.Model(&store.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", ts.ownerID).Count(&active).Error; err != nil {
		t.Fatalf("count live sessions: %v", err)
	}
	if active != 0 {
		t.Errorf("live sessions after the confirmed change = %d, want 0", active)
	}
	for i, cookies := range [][]*http.Cookie{ts.cookies, otherCookies} {
		rec := getWithCookies(t, ts.srv, "/api/v1/settings/email", cookies)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("session %d after the confirmed change = %d, want 401", i, rec.Code)
		}
	}
	found := false
	for _, n := range outboxByType(t, ts.db, mail.TypeCredentialChanged) {
		if n.To == "owner@example.com" && strings.Contains(n.TextBody, "email address") {
			found = true
		}
	}
	if !found {
		t.Error("no credential_changed notice to the replaced address")
	}
}
