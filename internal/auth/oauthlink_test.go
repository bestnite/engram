package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDecideIdentityLink is the acceptance matrix: table-driven coverage of the three automatic identity-linking branches,
// plus explicit coverage of four inputs (sub exists / verified email / unverified email / no email).
func TestDecideIdentityLink(t *testing.T) {
	matched := &store.User{ID: 11, Email: "alice@example.com"}
	bound := &store.Identity{ID: 1, UserID: 7, Provider: "issuer.example.com", Subject: "sub-bound"}

	cases := []struct {
		name       string
		profile    OAuthProfile
		existing   *store.Identity
		matched    *store.User
		policy     string
		wantKind   LinkDecisionKind
		wantUserID uint64
		wantErr    bool
	}{
		// Linking case 1: (provider, subject) already exists → log in directly, ignoring whether the email is verified.
		{
			name:       "sub already bound returns bound user",
			profile:    OAuthProfile{Provider: "issuer.example.com", Subject: "sub-bound", Email: "other@example.com", EmailVerified: true},
			existing:   bound,
			matched:    matched,
			policy:     PolicyClosed,
			wantKind:   LinkDecisionExisting,
			wantUserID: 7,
		},
		// Linking case 2: a verified email hits an internal user → link automatically (case-insensitive).
		{
			name:       "verified matching email auto links",
			profile:    OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "Alice@Example.com", EmailVerified: true},
			matched:    matched,
			policy:     PolicyClosed,
			wantKind:   LinkDecisionAutoLink,
			wantUserID: 11,
		},
		// Linking case 3: verified email but no account of that name → create an account per policy.
		{
			name:     "verified email without match creates under open",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "nobody@example.com", EmailVerified: true},
			policy:   PolicyOpen,
			wantKind: LinkDecisionCreateUser,
		},
		{
			name:     "verified email without match creates under invite",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "nobody@example.com", EmailVerified: true},
			policy:   PolicyInvite,
			wantKind: LinkDecisionCreateUser,
		},
		{
			name:     "verified email without match is denied under closed",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "nobody@example.com", EmailVerified: true},
			policy:   PolicyClosed,
			wantKind: LinkDecisionDeny,
			wantErr:  true,
		},
		// Unverified email: never auto-link; with no account of that name, follow the policy.
		{
			name:     "unverified email never auto links and creates under open",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "nobody@example.com", EmailVerified: false},
			policy:   PolicyOpen,
			wantKind: LinkDecisionCreateUser,
		},
		{
			name:     "unverified email matching an account is denied",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "alice@example.com", EmailVerified: false},
			matched:  matched,
			policy:   PolicyOpen,
			wantKind: LinkDecisionDeny,
			wantErr:  true,
		},
		// No email: auto-linking is impossible; under open an account is created (synthesizing a placeholder email on persist).
		{
			name:     "missing email creates under open",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "", EmailVerified: false},
			policy:   PolicyOpen,
			wantKind: LinkDecisionCreateUser,
		},
		{
			name:     "missing email is denied under closed",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new"},
			policy:   PolicyClosed,
			wantKind: LinkDecisionDeny,
			wantErr:  true,
		},
		// An unknown policy is handled by the safe closed default (reusing ParseRegistrationPolicy's normalization).
		{
			name:     "unknown policy denies",
			profile:  OAuthProfile{Provider: "issuer.example.com", Subject: "sub-new", Email: "nobody@example.com", EmailVerified: true},
			policy:   "weird",
			wantKind: LinkDecisionDeny,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideIdentityLink(tc.profile, tc.existing, tc.matched, tc.policy)
			if got.Kind != tc.wantKind {
				t.Fatalf("DecideIdentityLink() kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.UserID != tc.wantUserID {
				t.Errorf("DecideIdentityLink() userID = %d, want %d", got.UserID, tc.wantUserID)
			}
			if tc.wantErr {
				if !errors.Is(got.Err, ErrIdentityLinkDenied) {
					t.Errorf("DecideIdentityLink() err = %v, want %v", got.Err, ErrIdentityLinkDenied)
				}
				return
			}
			if got.Err != nil {
				t.Errorf("DecideIdentityLink() unexpected err = %v", got.Err)
			}
		})
	}
}

// linkEnv wires up real SQLite, real stores and the real service to verify the whole identity-linking flow (without mocking the database).
type linkEnv struct {
	db         *gorm.DB
	svc        *IdentityLinkService
	users      *store.UserStore
	identities *store.IdentityStore
	audit      *store.AuditStore
	accounts   *AccountService
}

func newLinkEnv(t *testing.T) *linkEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "link.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	users := store.NewUserStore(db)
	accounts, err := NewAccountService(users, store.NewSessionStore(db), store.NewAPIKeyStore(db), NewPasswordHasher(fastParams()))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	auditStore := store.NewAuditStore(db)
	auditor, err := NewAuditor(auditStore)
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	identities := store.NewIdentityStore(db)
	svc, err := NewIdentityLinkService(NewStoreIdentityLinkStore(identities, users, accounts), auditor, nil)
	if err != nil {
		t.Fatalf("NewIdentityLinkService() error = %v", err)
	}
	return &linkEnv{db: db, svc: svc, users: users, identities: identities, audit: auditStore, accounts: accounts}
}

func (e *linkEnv) seedLocalUser(t *testing.T, username string, reasonEmail string) *store.User {
	t.Helper()
	u, err := e.accounts.CreateLocalUser(context.Background(), CreateUserInput{
		Username: username, Email: reasonEmail, Password: "Tr0ub4dor&3",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%s) error = %v", username, err)
	}
	return u
}

// TestIdentityLinkServiceResolve covers the execution paths of the three identity-linking branches and verifies the unlink backend capability.
func TestIdentityLinkServiceResolve(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	alice := e.seedLocalUser(t, "alice", "alice@example.com")
	bob := e.seedLocalUser(t, "bob", "bob@example.com")

	// Seed one link for bob, used to verify the existing branch.
	if err := e.identities.Create(ctx, &store.Identity{
		UserID: bob.ID, Provider: "issuer.example.com", Subject: "sub-bob",
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}

	// Branch 1: sub already exists → log straight into the matching internal user, unaffected even by the closed policy.
	u, decision, err := e.svc.Resolve(ctx, OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-bob", Email: "bob@example.com", EmailVerified: true,
	}, PolicyClosed)
	if err != nil || u.ID != bob.ID || decision.Kind != LinkDecisionExisting {
		t.Fatalf("existing branch = (%+v, %q, %v), want bob/%q", u, decision.Kind, err, LinkDecisionExisting)
	}

	// Branch 2: verified email hits alice → link automatically and write an audit entry.
	u, decision, err = e.svc.Resolve(ctx, OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-alice", Email: "Alice@Example.com", EmailVerified: true,
	}, PolicyClosed)
	if err != nil || u.ID != alice.ID || decision.Kind != LinkDecisionAutoLink {
		t.Fatalf("auto-link branch = (%+v, %q, %v), want alice/%q", u, decision.Kind, err, LinkDecisionAutoLink)
	}
	linked, err := e.identities.ByProviderSubject(ctx, "issuer.example.com", "sub-alice")
	if err != nil || linked.UserID != alice.ID {
		t.Fatalf("identity not persisted for auto-link: %+v, err=%v", linked, err)
	}
	if n, err := e.audit.CountByAction(ctx, store.ActionIdentityLink); err != nil || n != 1 {
		t.Fatalf("identity.link audit rows = %d, err=%v, want 1", n, err)
	}

	// Branch 3: under the open policy nothing matches → create a new password-less account and link it.
	u, decision, err = e.svc.Resolve(ctx, OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-carol", Email: "carol@example.com", EmailVerified: true,
	}, PolicyOpen)
	if err != nil || decision.Kind != LinkDecisionCreateUser || u.PasswordHash != nil {
		t.Fatalf("create branch = (%+v, %q, %v), want create_user with nil password", u, decision.Kind, err)
	}
	if _, err := e.identities.ByProviderSubject(ctx, "issuer.example.com", "sub-carol"); err != nil {
		t.Fatalf("identity not persisted for created user: %v", err)
	}

	// Branch 3 negative: under the closed policy nothing matches → deny and create no account.
	_, decision, err = e.svc.Resolve(ctx, OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-dave", Email: "dave@example.com", EmailVerified: true,
	}, PolicyClosed)
	if !errors.Is(err, ErrIdentityLinkDenied) || decision.Kind != LinkDecisionDeny {
		t.Fatalf("closed branch = (%q, %v), want deny/%v", decision.Kind, err, ErrIdentityLinkDenied)
	}
	if _, err := e.identities.ByProviderSubject(ctx, "issuer.example.com", "sub-dave"); !errors.Is(err, store.ErrIdentityNotFound) {
		t.Fatalf("denied login must not create a binding, got err=%v", err)
	}

	// Deny the login when the link points at a disabled user.
	if err := e.accounts.DisableUser(ctx, alice.ID); err != nil {
		t.Fatalf("DisableUser() error = %v", err)
	}
	if _, _, err := e.svc.Resolve(ctx, OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-alice",
	}, PolicyClosed); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("disabled bound user error = %v, want %v", err, ErrUserDisabled)
	}

	// Unlink backend capability (used by the M6 admin panel; this round only verifies storage behavior).
	rows, err := e.identities.ListForUser(ctx, alice.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListForUser() = %d rows, err=%v, want 1", len(rows), err)
	}
	if err := e.identities.Delete(ctx, rows[0].ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := e.identities.Delete(ctx, rows[0].ID); !errors.Is(err, store.ErrIdentityNotFound) {
		t.Fatalf("second Delete() error = %v, want %v", err, store.ErrIdentityNotFound)
	}
}

// TestOIDCUsernameIsDeterministicAndUnique asserts that the login name for auto-created accounts is reproducible and that different identities do not collide.
func TestOIDCUsernameIsDeterministicAndUnique(t *testing.T) {
	p := OAuthProfile{Provider: "issuer.example.com", Subject: "sub-1", Email: "Alice@Example.com"}
	if got, want := oidcUsername(p), oidcUsername(p); got != want {
		t.Errorf("oidcUsername() not deterministic: %q vs %q", got, want)
	}
	other := OAuthProfile{Provider: "issuer.example.com", Subject: "sub-2", Email: "Alice@Example.com"}
	if oidcUsername(p) == oidcUsername(other) {
		t.Errorf("oidcUsername() collision for different subjects: %q", oidcUsername(p))
	}
	noEmail := OAuthProfile{Provider: "issuer.example.com", Subject: "sub-3"}
	if got := oidcAccountEmail(noEmail); got == "" {
		t.Error("oidcAccountEmail() empty for a profile without an email claim")
	}
}
