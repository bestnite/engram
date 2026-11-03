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
//   - Periodic task history: a jobs row written by a periodic round (trigger auto or manual) is kept for JobRunRetention after it finished.
//     Optimiser rows are not touched: they are a user's record of a parameter change, not operational noise.
//   - Media: media that no note references first gets an orphaned timestamp recorded, and once orphaned for longer than MediaOrphanGrace it is deleted together with its file.
//     The grace period leaves a window for "deleted an image while editing and put it back" and for "uploaded but not yet inserted into a card".
//
// Media reclamation is its own task (worker.KindMediaGC) beside the row reclamation (worker.KindRetention): the two have unrelated
// failure modes (a file system against row deletes), and an administrator who starts one by hand from the jobs panel should not run the other.
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
	// ActionTokenGrace is how long a token is kept after expiry (30 days).
	ActionTokenGrace = 30 * 24 * time.Hour
	// FingerprintRetention is how long a login fingerprint is kept after it was last seen (90 days).
	FingerprintRetention = 90 * 24 * time.Hour
	// MediaOrphanGrace is how long media is kept after it becomes unreferenced (7 days).
	MediaOrphanGrace = 7 * 24 * time.Hour
	// JobRunRetention is how long a periodic task's history row is kept after it finished (30 days).
	JobRunRetention = 30 * 24 * time.Hour
)

// Expirer is the minimal ability to "delete expired rows by time", implemented by the store's session/token/invite types.
type Expirer interface {
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// StalePurger is the minimal ability to "delete rows older than a time", implemented by the login fingerprint store
// (by last-seen time) and the jobs store (periodic history rows by finish time).
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
	JobRuns      StalePurger
	Media        MediaCollector
	Logger       *slog.Logger
	// Interval is the reclamation interval of both workers; the worker's ten-minute default is used when <=0.
	Interval time.Duration
}

// Workers are the two reclamation tasks New builds.
type Workers struct {
	// Records deletes expired and stale rows (worker.KindRetention).
	Records *worker.Lifecycle
	// Media reclaims orphaned media (worker.KindMediaGC).
	Media *worker.Lifecycle
}

// New constructs the reclamation workers; it does not start them, call Start on each afterwards.
func New(deps Deps) (Workers, error) {
	switch {
	case deps.Sessions == nil:
		return Workers{}, errors.New("retention: Deps.Sessions is required")
	case deps.ActionTokens == nil:
		return Workers{}, errors.New("retention: Deps.ActionTokens is required")
	case deps.Invites == nil:
		return Workers{}, errors.New("retention: Deps.Invites is required")
	case deps.ShareInvites == nil:
		return Workers{}, errors.New("retention: Deps.ShareInvites is required")
	case deps.Fingerprints == nil:
		return Workers{}, errors.New("retention: Deps.Fingerprints is required")
	case deps.JobRuns == nil:
		return Workers{}, errors.New("retention: Deps.JobRuns is required")
	case deps.Media == nil:
		return Workers{}, errors.New("retention: Deps.Media is required")
	case deps.Logger == nil:
		return Workers{}, errors.New("retention: Deps.Logger is required")
	}
	records, err := worker.New(worker.Config{
		Kind:     worker.KindRetention,
		Interval: deps.Interval,
		Logger:   deps.Logger,
		RunOnce:  func(ctx context.Context) (worker.Summary, error) { return sweep(ctx, deps) },
		// A failed round is logged by the worker and processing continues: being unable to delete one category should not hold up the others.
		FailMessage: "retention: sweep failed",
	})
	if err != nil {
		return Workers{}, err
	}
	mediaWorker, err := worker.New(worker.Config{
		Kind:        worker.KindMediaGC,
		Interval:    deps.Interval,
		Logger:      deps.Logger,
		RunOnce:     func(ctx context.Context) (worker.Summary, error) { return collectMedia(ctx, deps) },
		FailMessage: "retention: media collection failed",
	})
	if err != nil {
		return Workers{}, err
	}
	return Workers{Records: records, Media: mediaWorker}, nil
}

// sweep runs one row reclamation round: the categories are independent and each failure is recorded (returned together via
// errors.Join); the counts of rows successfully deleted are still reported, so logging is not dropped because another category failed.
// The summary is keyed by table name, with job_runs for the periodic history rows.
func sweep(ctx context.Context, deps Deps) (worker.Summary, error) {
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
		{"job_runs", now.Add(-JobRunRetention), deps.JobRuns.DeleteStale},
	}

	summary := worker.Summary{}
	var errs []error
	for _, t := range targets {
		removed, err := t.run(ctx, t.before)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.table, err))
			continue
		}
		summary[t.table] = removed
		// Log a line only when something was actually deleted: an ordinary no-op round should not produce log noise.
		if removed > 0 {
			deps.Logger.Info("expired rows removed", "table", t.table, "count", removed)
		}
	}
	return summary, errors.Join(errs...)
}

// collectMedia runs one media reclamation round; the summary's removed counts the media whose file was reclaimed.
func collectMedia(ctx context.Context, deps Deps) (worker.Summary, error) {
	removed, err := deps.Media.CollectGarbage(ctx, time.Now().UTC(), MediaOrphanGrace)
	if removed > 0 {
		deps.Logger.Info("orphaned media removed", "count", removed)
	}
	summary := worker.Summary{"removed": removed}
	if err != nil {
		return summary, fmt.Errorf("media: %w", err)
	}
	return summary, nil
}
