package auth

import (
	"context"
	"errors"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestIdentityLinkServiceBind covers the self-service bind path: attaching an identity to the caller's account, idempotency,
// and the refusal when the pair already names another account.
func TestIdentityLinkServiceBind(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	alice := e.seedLocalUser(t, "alice", "alice@example.com")
	bob := e.seedLocalUser(t, "bob", "bob@example.com")
	profile := OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "Alice@Example.com", EmailVerified: true}

	if err := e.svc.Bind(ctx, profile, alice.ID); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	ident, err := e.identities.ByProviderSubject(ctx, profile.Provider, profile.Subject)
	if err != nil || ident.UserID != alice.ID {
		t.Fatalf("bound identity = %+v, err=%v, want alice", ident, err)
	}
	if ident.Email == nil || *ident.Email != "alice@example.com" {
		t.Fatalf("bound email = %v, want the normalized profile email", ident.Email)
	}
	if n, err := e.audit.CountByAction(ctx, store.ActionIdentityLink); err != nil || n != 1 {
		t.Fatalf("identity.link audit rows = %d (err %v), want 1", n, err)
	}

	// Same user again: idempotent, no second row and no second audit row.
	if err := e.svc.Bind(ctx, profile, alice.ID); err != nil {
		t.Fatalf("repeated Bind() error = %v", err)
	}
	if rows, err := e.identities.ListForUser(ctx, alice.ID); err != nil || len(rows) != 1 {
		t.Fatalf("identities after repeated bind = %d (err %v), want 1", len(rows), err)
	}
	if n, err := e.audit.CountByAction(ctx, store.ActionIdentityLink); err != nil || n != 1 {
		t.Fatalf("identity.link audit rows after repeated bind = %d (err %v), want 1", n, err)
	}

	// Another account trying the same pair is refused and writes nothing.
	if err := e.svc.Bind(ctx, profile, bob.ID); !errors.Is(err, ErrIdentityAlreadyLinked) {
		t.Fatalf("Bind() for another user error = %v, want %v", err, ErrIdentityAlreadyLinked)
	}
	if rows, err := e.identities.ListForUser(ctx, bob.ID); err != nil || len(rows) != 0 {
		t.Fatalf("bob identities = %d (err %v), want 0", len(rows), err)
	}
}

// TestIdentityLinkServiceBindKeepsRawSubject asserts that trimming is only used to reject empty values: the raw provider and subject are the
// identity keys everywhere in the flow, exactly as in Resolve, so whitespace variants are distinct identities rather than the same one.
func TestIdentityLinkServiceBindKeepsRawSubject(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	alice := e.seedLocalUser(t, "alice", "alice@example.com")

	for _, subject := range []string{"abc", " abc "} {
		if err := e.svc.Bind(ctx, OAuthProfile{Provider: "issuer.example.com", Subject: subject}, alice.ID); err != nil {
			t.Fatalf("Bind(%q) error = %v", subject, err)
		}
	}
	rows, err := e.identities.ListForUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("identities = %d, want 2 distinct subjects", len(rows))
	}
	for _, want := range []string{"abc", " abc "} {
		if _, err := e.identities.ByProviderSubject(ctx, "issuer.example.com", want); err != nil {
			t.Fatalf("identity with subject %q not stored: %v", want, err)
		}
	}
}

// TestIdentityLinkServiceBindRejectsEmptyProfile covers the validation of the normalized profile input.
func TestIdentityLinkServiceBindRejectsEmptyProfile(t *testing.T) {
	e := newLinkEnv(t)
	alice := e.seedLocalUser(t, "alice", "alice@example.com")
	if err := e.svc.Bind(context.Background(), OAuthProfile{Provider: "issuer.example.com"}, alice.ID); err == nil {
		t.Fatal("Bind() without a subject returned nil, want an error")
	}
}
