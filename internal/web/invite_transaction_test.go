package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// TestInviteAcceptFailureKeepsTokenUsable is HTTP-level negative acceptance:
// when creating the account from an invite fails, the token must still be usable, and no "used → usable again" intermediate state may be exposed.
// The failure is triggered by a username conflict (same name as the existing admin) — a real account-creation failure, not a fabricated one.
func TestInviteAcceptFailureKeepsTokenUsable(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv) // creates admin / admin@example.com so a same-name signup must conflict
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "invite")))
	invites := store.NewInviteStore(db)

	inv := &store.Invite{}
	if err := invites.Create(context.Background(), inv); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// The username conflicts with the existing admin: account creation fails and the whole accept transaction rolls back.
	cookie, headers := preSessionPair(t, srv, "/register")
	failed := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "Sup3rSecret!",
		"invite":   inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if failed.Code != http.StatusConflict {
		t.Fatalf("POST /api/v1/auth/register with a failing create = %d, want 409 (body %s)", failed.Code, snippet(failed.Body.String()))
	}

	// After the rollback the token has not been consumed: used_at is still empty and the admin can use it again.
	after, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if after.UsedAt != nil || after.UsedBy != nil {
		t.Fatalf("invite after a failed accept = %+v, want used_at and used_by still nil", after)
	}

	// The same token with a different username is accepted normally, and used_by points at the new user.
	retry := postJSON(srv, "/api/v1/auth/register", map[string]string{
		"username": "invitee",
		"email":    "invitee@example.com",
		"password": "Sup3rSecret!",
		"invite":   inv.Token,
	}, []*http.Cookie{cookie}, headers)
	if retry.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/auth/register after the rollback = %d, want 200 (body %s)", retry.Code, snippet(retry.Body.String()))
	}
	used, err := invites.ByToken(context.Background(), inv.Token)
	if err != nil {
		t.Fatalf("ByToken() error = %v", err)
	}
	if used.UsedAt == nil || used.UsedBy == nil {
		t.Fatalf("invite after a successful accept = %+v, want used_at and used_by set", used)
	}
}
