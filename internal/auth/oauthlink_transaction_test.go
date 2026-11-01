package auth

import (
	"context"
	"sync"
	"testing"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// TestResolveRollsBackTheAccountWhenTheLinkFails asserts that the first-login account and its identity link are one
// write: when the identity insert is refused, the account is not left behind without a way to reach it.
func TestResolveRollsBackTheAccountWhenTheLinkFails(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	profile := OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-broken-link",
		Email: "broken@example.com", EmailVerified: true,
	}
	if err := e.db.Exec(`CREATE TRIGGER fail_identity_insert BEFORE INSERT ON identities
		BEGIN SELECT RAISE(ABORT, 'injected identity failure'); END`).Error; err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	if _, _, err := e.svc.Resolve(ctx, profile, PolicyOpen); err == nil {
		t.Fatal("Resolve() error = nil, want the injected link failure")
	}
	var users, identities int64
	if err := e.db.Model(&store.User{}).Where("username = ?", oidcUsername(profile)).Count(&users).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := e.db.Model(&store.Identity{}).Count(&identities).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if users != 0 {
		t.Errorf("users after the failed link = %d, want 0: the account must roll back with its identity", users)
	}
	if identities != 0 {
		t.Errorf("identities after the failed link = %d, want 0", identities)
	}

	if err := e.db.Exec(`DROP TRIGGER fail_identity_insert`).Error; err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	u, decision, err := e.svc.Resolve(ctx, profile, PolicyOpen)
	if err != nil {
		t.Fatalf("Resolve() after the trigger was removed error = %v, want nil", err)
	}
	if decision.Kind != LinkDecisionCreateUser {
		t.Errorf("Resolve() decision = %q, want %q", decision.Kind, LinkDecisionCreateUser)
	}
	if again, err := e.identities.ByProviderSubject(ctx, profile.Provider, profile.Subject); err != nil || again.UserID != u.ID {
		t.Fatalf("identity after the retry = %+v, err=%v, want user %d", again, err, u.ID)
	}
}

// TestResolveAdoptsAnAccountWhoseLinkFailed asserts the recovery path for an account an earlier attempt created
// without its link: the deterministic username identifies it and a matching email confirms it, so the next login
// links it instead of failing forever on the unique username.
func TestResolveAdoptsAnAccountWhoseLinkFailed(t *testing.T) {
	ctx := context.Background()
	e := newLinkEnv(t)
	profile := OAuthProfile{Provider: "issuer.example.com", Subject: "sub-orphan"}
	orphan, err := e.accounts.CreateOIDCUser(ctx, CreateUserInput{
		Username: oidcUsername(profile), Email: oidcAccountEmail(profile),
	})
	if err != nil {
		t.Fatalf("seed the account without its link: %v", err)
	}

	u, _, err := e.svc.Resolve(ctx, profile, PolicyOpen)
	if err != nil {
		t.Fatalf("Resolve() error = %v, want the existing account adopted", err)
	}
	if u.ID != orphan.ID {
		t.Errorf("Resolve() user id = %d, want the seeded account %d", u.ID, orphan.ID)
	}
	if again, err := e.identities.ByProviderSubject(ctx, profile.Provider, profile.Subject); err != nil || again.UserID != orphan.ID {
		t.Fatalf("identity after adoption = %+v, err=%v, want user %d", again, err, orphan.ID)
	}
}

// TestResolveConcurrentFirstLoginsBothSucceed asserts that two simultaneous first logins for the same identity both
// end in the same account: the loser of the account insert recovers through the winner's link instead of failing.
// PostgreSQL only: the window needs two independent write transactions.
func TestResolveConcurrentFirstLoginsBothSucceed(t *testing.T) {
	db, ok := pgtest.Open(t)
	if !ok {
		t.Skip("TEST_PG_DSN not set: the race needs two PostgreSQL connections")
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	e := newLinkEnvAt(t, db)
	profile := OAuthProfile{
		Provider: "issuer.example.com", Subject: "sub-concurrent",
		Email: "concurrent@example.com", EmailVerified: true,
	}
	// Both logins must pass their lookups before either account insert commits; the delay widens that window so the
	// case fails reliably when the loser of the insert has no recovery path.
	if err := db.Exec(`CREATE FUNCTION slow_user_insert() RETURNS trigger AS $$
		BEGIN
			PERFORM pg_sleep(0.3);
			RETURN NEW;
		END $$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatalf("create insert delay: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER slow_user_insert BEFORE INSERT ON users
		FOR EACH ROW EXECUTE FUNCTION slow_user_insert()`).Error; err != nil {
		t.Fatalf("create insert delay trigger: %v", err)
	}

	start := make(chan struct{})
	users := make([]*store.User, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			users[i], _, errs[i] = e.svc.Resolve(context.Background(), profile, PolicyOpen)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Resolve() #%d error = %v, want nil", i, err)
		}
	}
	if users[0] == nil || users[1] == nil || users[0].ID != users[1].ID {
		t.Fatalf("concurrent Resolve() users = (%v, %v), want both to resolve the same account", users[0], users[1])
	}
	var userCount, identityCount int64
	if err := db.Model(&store.User{}).Where("username = ?", oidcUsername(profile)).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.Model(&store.Identity{}).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if userCount != 1 || identityCount != 1 {
		t.Errorf("rows after both logins = %d users, %d identities, want 1 and 1", userCount, identityCount)
	}
}
