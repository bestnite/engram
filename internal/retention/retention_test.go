package retention

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// Per the rule in AGENTS.md §2.5 this package uses a real database (a sqlite file) instead of mocks: the retention boundaries are exactly what needs verifying.

type fixture struct {
	sessions     *store.SessionStore
	tokens       *store.ActionTokenStore
	invites      *store.InviteStore
	shareInvites *store.DeckShareInviteStore
	fingerprints *store.LoginFingerprintStore
	media        *media.Store
	logs         *bytes.Buffer
	db           *gorm.DB
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	mediaStore, err := media.New(filepath.Join(t.TempDir(), "media"), db)
	if err != nil {
		t.Fatalf("media.New() error = %v", err)
	}
	return fixture{
		media:        mediaStore,
		sessions:     store.NewSessionStore(db),
		tokens:       store.NewActionTokenStore(db),
		invites:      store.NewInviteStore(db),
		shareInvites: store.NewDeckShareInviteStore(db),
		fingerprints: store.NewLoginFingerprintStore(db),
		logs:         &bytes.Buffer{},
		db:           db,
	}
}

// deps assembles the dependencies needed for one reclamation round, with logs collected into the fixture's buffer.
func (f fixture) deps() Deps {
	return Deps{
		Sessions:     f.sessions,
		ActionTokens: f.tokens,
		Invites:      f.invites,
		ShareInvites: f.shareInvites,
		Fingerprints: f.fingerprints,
		Media:        f.media,
		Logger:       slog.New(slog.NewTextHandler(f.logs, nil)),
		Interval:     time.Hour,
	}
}

// sweepOnce runs one reclamation round synchronously and asserts that it reports no error.
//
// It deliberately calls sweep directly instead of going through Start/Stop: the latter is asynchronous and Stop cancels the context
// before the round finishes, so the assertion would race the scheduler (this once bit us — all four delete categories reported context canceled).
func (f fixture) sweepOnce(t *testing.T) {
	t.Helper()
	if err := sweep(context.Background(), f.deps()); err != nil {
		t.Fatalf("sweep() error = %v", err)
	}
}

// TestWorkerRunsASweepOnStart pins down that "the wiring really runs": the worker runs a round on start, so an expired session
// should disappear shortly after startup. This is the end-to-end expression of the Start contract (run one round on start) in this package.
func TestWorkerRunsASweepOnStart(t *testing.T) {
	f := newFixture(t)
	f.addSession(t, "session-expired", -time.Minute)

	lifecycle, err := New(f.deps())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle.Start(ctx)
	defer lifecycle.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := f.sessions.ByID(context.Background(), "session-expired"); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("starting the worker did not reap the expired session")
}

// pinLocalZone replaces the process time.Local with a fixed offset and restores it when the test ends.
//
// Why: this class of defect (boundary value carrying the local offset → SQLite string comparison shifts the whole window) only shows up
// on a machine where "local offset != 0", while CI runs on UTC year-round — without a fixed timezone the regression cannot be caught. Rows are
// stored in the writer's real timezone (UTC), so as soon as the boundary value reverts to local time the assertions below must go red.
func pinLocalZone(t *testing.T, offsetHours int) {
	t.Helper()
	original := time.Local
	time.Local = time.FixedZone("TST", offsetHours*3600)
	t.Cleanup(func() { time.Local = original })
}

// addSession writes one session row.
func (f fixture) addSession(t *testing.T, id string, expiresIn time.Duration) {
	t.Helper()
	if err := f.sessions.Create(context.Background(), &store.Session{
		ID: id, UserID: 1, CSRFToken: "t-" + id,
		CreatedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(expiresIn),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

// addToken writes one token row and returns its plaintext (for Peek to check).
func (f fixture) addToken(t *testing.T, purpose string, expiresIn time.Duration) string {
	t.Helper()
	plaintext := purpose + "-" + expiresIn.String()
	if err := f.tokens.Create(context.Background(), &store.ActionToken{
		Purpose: purpose, UserID: 1, TokenHash: store.HashActionToken(plaintext),
		CreatedAt: time.Now().UTC().Add(-24 * time.Hour), ExpiresAt: time.Now().UTC().Add(expiresIn),
	}); err != nil {
		t.Fatalf("create action token: %v", err)
	}
	return plaintext
}

// TestSweepRespectsEachRetentionWindow is this package's core assertion: the retention boundary of each of the four row categories.
func TestSweepRespectsEachRetentionWindow(t *testing.T) {
	pinLocalZone(t, 8)
	f := newFixture(t)
	ctx := context.Background()

	// Sessions: delete on expiry.
	f.addSession(t, "session-expired", -time.Minute)
	f.addSession(t, "session-live", time.Hour)

	// Tokens: keep for ActionTokenGrace after expiry.
	pastGrace := f.addToken(t, "password_reset", -ActionTokenGrace-time.Minute) // should be deleted
	inGrace := f.addToken(t, "password_reset", -ActionTokenGrace+time.Hour)     // just expired, still within the grace period
	notExpired := f.addToken(t, "email_verification", time.Hour)                // not expired

	// Invites: delete on expiry.
	expiredAt := time.Now().UTC().Add(-time.Hour)
	staleInvite := store.Invite{Token: "invite-expired", Role: "user", CreatedAt: time.Now().UTC().Add(-48 * time.Hour), ExpiresAt: &expiredAt}
	if err := f.invites.Create(ctx, &staleInvite); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	futureAt := time.Now().UTC().Add(24 * time.Hour)
	liveInvite := store.Invite{Token: "invite-live", Role: "user", CreatedAt: time.Now().UTC(), ExpiresAt: &futureAt}
	if err := f.invites.Create(ctx, &liveInvite); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// Deck share invites: delete on expiry (the unexpired one must stay — it is still "pending acceptance").
	seedShareInvite(t, f, 7, 11, -time.Hour) // expired, should be deleted
	seedShareInvite(t, f, 8, 12, time.Hour)  // one hour left, keep

	// Login fingerprints: deleted only once last seen is older than FingerprintRetention.
	if _, err := f.fingerprints.Touch(ctx, 1, "stale-device", time.Now().Add(-FingerprintRetention-time.Hour)); err != nil {
		t.Fatalf("touch stale-device: %v", err)
	}
	if _, err := f.fingerprints.Touch(ctx, 1, "recent-device", time.Now().Add(-FingerprintRetention+time.Hour)); err != nil {
		t.Fatalf("touch recent-device: %v", err)
	}

	f.sweepOnce(t)

	// Deck share invites: the expired one is gone, the unexpired one remains (the read path also judges expiry; this verifies reclamation really deleted it).
	if _, err := f.shareInvites.ByDeckAndUser(ctx, 7, 11); !errors.Is(err, store.ErrShareInviteNotFound) {
		t.Errorf("expired deck share invite should be gone, got err = %v", err)
	}
	if _, err := f.shareInvites.ByDeckAndUser(ctx, 8, 12); err != nil {
		t.Errorf("live deck share invite must survive: %v", err)
	}

	// Sessions: the expired one is gone, the valid one remains.
	if _, err := f.sessions.ByID(ctx, "session-expired"); err == nil {
		t.Error("expired session should be gone")
	}
	if _, err := f.sessions.ByID(ctx, "session-live"); err != nil {
		t.Errorf("live session must survive: %v", err)
	}

	// Tokens: only the one past the grace period was deleted (the other two can still be found by Peek — the expired one reports expired, the unexpired one is returned directly).
	if _, err := f.tokens.Peek(ctx, "password_reset", pastGrace, time.Now()); !errors.Is(err, store.ErrActionTokenNotFound) {
		t.Errorf("token past the grace period should be gone, got err = %v", err)
	}
	if _, err := f.tokens.Peek(ctx, "password_reset", inGrace, time.Now()); !errors.Is(err, store.ErrActionTokenExpired) {
		t.Errorf("token inside the grace period should still exist but read as expired, got err = %v", err)
	}
	if _, err := f.tokens.Peek(ctx, "email_verification", notExpired, time.Now()); err != nil {
		t.Errorf("unexpired token should still be readable: %v", err)
	}

	// Invites: the expired one is gone, the unexpired one remains.
	rows, err := f.invites.List(ctx)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	var tokens []string
	for _, r := range rows {
		tokens = append(tokens, r.Token)
	}
	if len(tokens) != 1 || tokens[0] != "invite-live" {
		t.Errorf("invites after sweep = %v, want only invite-live", tokens)
	}

	// Fingerprints: the deleted one is judged a new device, the kept one a known device.
	knownRecent, err := f.fingerprints.Touch(ctx, 1, "recent-device", time.Now())
	if err != nil {
		t.Fatalf("touch recent-device: %v", err)
	}
	if !knownRecent {
		t.Error("a device seen within the retention window should still count as known")
	}
	knownStale, err := f.fingerprints.Touch(ctx, 1, "stale-device", time.Now())
	if err != nil {
		t.Fatalf("touch stale-device: %v", err)
	}
	if knownStale {
		t.Error("a device last seen before the retention window should count as new again")
	}
}

// TestSweepKeepsInvitesWithoutExpiry pins down the sharp edge of a nullable expires_at on its own:
// NULL means "never expires" and must not be cleared as expired.
func TestSweepKeepsInvitesWithoutExpiry(t *testing.T) {
	pinLocalZone(t, 8)
	f := newFixture(t)
	ctx := context.Background()
	never := store.Invite{Token: "never-expires", Role: "user", CreatedAt: time.Now().UTC().Add(-365 * 24 * time.Hour)}
	if err := f.invites.Create(ctx, &never); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// Rows are stored in the writer's real timezone (UTC) — storing UTC in time columns is the consistent convention for these tables;
	// a fixture writing local time would make an "expired row" look not-yet-expired when compared against the UTC boundary value.
	past := time.Now().UTC().Add(-time.Hour)
	stale := store.Invite{Token: "expired", Role: "user", CreatedAt: time.Now().UTC().Add(-48 * time.Hour), ExpiresAt: &past}
	if err := f.invites.Create(ctx, &stale); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	f.sweepOnce(t)

	rows, err := f.invites.List(ctx)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	var tokens []string
	for _, r := range rows {
		tokens = append(tokens, r.Token)
	}
	if len(tokens) != 1 || tokens[0] != "never-expires" {
		t.Errorf("invites after sweep = %v, want only never-expires", tokens)
	}
}

// TestNewRequiresEveryStore asserts that missing any of the four stores fails assembly.
func TestNewRequiresEveryStore(t *testing.T) {
	f := newFixture(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	base := Deps{
		Sessions: f.sessions, ActionTokens: f.tokens, Invites: f.invites,
		ShareInvites: f.shareInvites, Fingerprints: f.fingerprints, Media: f.media, Logger: logger,
	}
	if _, err := New(base); err != nil {
		t.Fatalf("New() with all deps error = %v", err)
	}
	for name, mutate := range map[string]func(*Deps){
		"sessions":      func(d *Deps) { d.Sessions = nil },
		"tokens":        func(d *Deps) { d.ActionTokens = nil },
		"invites":       func(d *Deps) { d.Invites = nil },
		"share_invites": func(d *Deps) { d.ShareInvites = nil },
		"fingerprints":  func(d *Deps) { d.Fingerprints = nil },
		"media":         func(d *Deps) { d.Media = nil },
		"logger":        func(d *Deps) { d.Logger = nil },
	} {
		d := base
		mutate(&d)
		if _, err := New(d); err == nil {
			t.Errorf("New() without %s should fail", name)
		}
	}
}

// seedShareInvite writes a deck share invite directly: this case only verifies the reclamation window and does not need real decks and users
// (these two columns have no foreign key constraints, so any id value works).
func seedShareInvite(t *testing.T, f fixture, deckID, userID uint64, ttl time.Duration) {
	t.Helper()
	now := time.Now().UTC()
	err := f.shareInvites.Invite(context.Background(), store.DeckShareInvite{
		DeckID: deckID, UserID: userID, Role: store.RoleReader, InvitedBy: 1,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		t.Fatalf("seed share invite: %v", err)
	}
}
