// Package retention periodically reclaims the various "append-only" history rows.
//
// Why it is needed: several kinds of row are judged expired/stale only on the read path and are never deleted — session rows only get
// revoked_at written on logout/revocation, and expiry is judged only in Manager.Middleware; email verification/reset/unsubscribe tokens
// stay in action_tokens after use or expiry; expired invites stay in invites; login fingerprints accumulate one row per
// (IP, device) each user has ever been seen from.
// Without this reclamation layer these tables only grow with use and never shrink.
//
// The skeleton mirrors internal/reminder and internal/digest: it reuses worker.Lifecycle's start/stop and tick loop,
// and this package only supplies "what to scan, how long to keep it".
//
// Retention periods are policy, centralized in the constants below, changeable in one place:
//
//   - Sessions: delete on expiry (the read path no longer accepts them; the only thing keeping them around is space).
//   - Tokens (verification/reset/unsubscribe): keep for ActionTokenGrace after expiry, to make "link expired" feedback easier to investigate.
//   - Invites: delete on expiry; an invite with expires_at NULL never expires and is never deleted.
//   - Login fingerprints: keep for FingerprintRetention after last seen — deleting too early would misjudge an old device as new and trigger a reminder.
//   - Deck share invites: delete on expiry (the read path also judges expiry; deleting just stops the table from growing).
//   - Media: media that no note references first gets an orphaned timestamp recorded, and once orphaned for longer than MediaOrphanGrace it is deleted together with its file.
//     The grace period leaves a window for "deleted an image while editing and put it back" and for "uploaded but not yet inserted into a card".
//
// **API keys are deliberately not deleted automatically**: they are user-visible assets the user can delete, and their count is on the
// order of the user count (not a growth source); automatically deleting a key that is "expired but still in the list" would be deciding for the user.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.nite07.com/nite/engram/internal/worker"
)

const (
	// DefaultInterval is the default reclamation interval (1 hour).
	//
	// Rationale: everything reclaimed is an **already expired/stale** row, so deleting it a little later has no user-perceptible
	// consequence, and there is no need for a ten-minute round like reminder/digest. The relevant time columns are indexed, so a single delete is independent of table size.
	DefaultInterval = time.Hour
	// ActionTokenGrace is how long a token is kept after expiry (30 days).
	ActionTokenGrace = 30 * 24 * time.Hour
	// FingerprintRetention is how long a login fingerprint is kept after it was last seen (90 days).
	FingerprintRetention = 90 * 24 * time.Hour
	// MediaOrphanGrace is how long media is kept after it becomes unreferenced (7 days).
	MediaOrphanGrace = 7 * 24 * time.Hour
)

// Expirer is the minimal ability to "delete expired rows by time", implemented by the store's session/token/invite types.
type Expirer interface {
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// StalePurger is the minimal ability to "delete by last-seen time", implemented by the login fingerprint store.
type StalePurger interface {
	DeleteStale(ctx context.Context, before time.Time) (int64, error)
}

// MediaCollector is the minimal ability to "reclaim orphaned media", implemented by internal/media's Store.
type MediaCollector interface {
	CollectGarbage(ctx context.Context, now time.Time, grace time.Duration) (int64, error)
}

// Deps holds New's construction arguments; every store is required (a missing one would reclaim one category less, which is an assembly error, so failing early is better).
type Deps struct {
	Sessions     Expirer
	ActionTokens Expirer
	Invites      Expirer
	ShareInvites Expirer
	Fingerprints StalePurger
	Media        MediaCollector
	Logger       *slog.Logger
	// Interval is the reclamation interval; DefaultInterval is used when <=0.
	Interval time.Duration
}

// New constructs the reclamation worker; it does not start it, call Start afterwards.
func New(deps Deps) (*worker.Lifecycle, error) {
	switch {
	case deps.Sessions == nil:
		return nil, errors.New("retention: Deps.Sessions is required")
	case deps.ActionTokens == nil:
		return nil, errors.New("retention: Deps.ActionTokens is required")
	case deps.Invites == nil:
		return nil, errors.New("retention: Deps.Invites is required")
	case deps.ShareInvites == nil:
		return nil, errors.New("retention: Deps.ShareInvites is required")
	case deps.Fingerprints == nil:
		return nil, errors.New("retention: Deps.Fingerprints is required")
	case deps.Media == nil:
		return nil, errors.New("retention: Deps.Media is required")
	case deps.Logger == nil:
		return nil, errors.New("retention: Deps.Logger is required")
	}
	return worker.New(worker.Config{
		Interval: deps.Interval,
		Logger:   deps.Logger,
		RunOnce:  func(ctx context.Context) error { return sweep(ctx, deps) },
		// A failed round is logged by the worker and processing continues: being unable to delete one category should not hold up the other four.
		FailMessage: "retention: sweep failed",
	})
}

// sweep runs one reclamation round: the categories are independent and each failure is recorded (returned together via
// errors.Join); the counts of rows successfully deleted are still reported, so logging is not dropped because another category failed.
func sweep(ctx context.Context, deps Deps) error {
	// **The boundary values must be UTC**: the time columns of these tables are all written in UTC (auth.Manager.now,
	// ActionTokenService.now, invite creation and fingerprint Touch all call .UTC() explicitly), while SQLite compares times as
	// strings and does not honour timezone offsets — passing boundary values in the local timezone shifts the whole window by a UTC
	// offset (measured on UTC+8, it deleted rows still a day away from expiry). This matches the existing convention of passing
	// now.UTC() in store/health.go, store/reminder.go and elsewhere. PostgreSQL columns are real time types and are unaffected.
	now := time.Now().UTC()
	targets := []struct {
		table  string
		before time.Time
		run    func(context.Context, time.Time) (int64, error)
	}{
		{"sessions", now, deps.Sessions.DeleteExpired},
		{"action_tokens", now.Add(-ActionTokenGrace), deps.ActionTokens.DeleteExpired},
		{"invites", now, deps.Invites.DeleteExpired},
		{"deck_share_invites", now, deps.ShareInvites.DeleteExpired},
		{"login_fingerprints", now.Add(-FingerprintRetention), deps.Fingerprints.DeleteStale},
	}

	var errs []error
	for _, t := range targets {
		removed, err := t.run(ctx, t.before)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.table, err))
			continue
		}
		// Log a line only when something was actually deleted: an ordinary hourly no-op should not produce log noise.
		if removed > 0 {
			deps.Logger.Info("expired rows removed", "table", t.table, "count", removed)
		}
	}
	removed, err := deps.Media.CollectGarbage(ctx, now, MediaOrphanGrace)
	if err != nil {
		errs = append(errs, fmt.Errorf("media: %w", err))
	}
	if removed > 0 {
		deps.Logger.Info("orphaned media removed", "count", removed)
	}
	return errors.Join(errs...)
}
