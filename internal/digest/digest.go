// Package digest implements the periodic background worker for the C-class "weekly study digest".
//
// Behavior constraints:
//   - The digest runs **weekly** by default (not daily), aligned to the user's local timezone; it can be disabled (C-class defaults to off).
//   - **Exactly one per user per week**: the ledger lives in store.DigestLog; the primary key (user_id, week_start) is the last
//     line of defense that still holds across process restarts — an in-process counter resets on restart, and the week's digest would be sent again.
//   - Never send mail synchronously on the request path: always Enqueue, delivery is handled by the outbox worker.
//   - The send hour is shared with review reminders (users.reminder_hour, falling back to reminder.DefaultSendHour when unset):
//     the first poll after reaching that hour sends, and the hour is interpreted in the user's timezone.
//   - When SMTP is not configured, neither send nor error; just log one English line.
//   - The digest is an optional type and carries the RFC 8058 one-click unsubscribe header.
//
// The quiet window (the old 23:00–07:00) was removed together with the review reminder: the send time is now
// explicitly chosen by the user (default 19:00), and the quiet window served no purpose while suppressing late-night hours
// the user explicitly picked; see the package comment of internal/reminder for details.
//
// Number sourcing: every number shown in the digest comes directly from the internal/store StatsStore aggregate methods
// (the same calls the stats page makes), and neither the handler nor the worker writes statistics SQL. The window matches the
// stats page's "last 7 days": [today-6, today] (review_day inclusive).
//
// Why a separate worker instead of reusing the reminder worker: the two have different rhythms (daily vs weekly), different
// candidate sets (people with due cards vs everyone who can receive mail), and different copy and dedup ledgers; forcing them
// into one worker would entangle the "reminder" and "digest" decisions. Both are just periodic small scans and do not occupy
// the optimizer's single-concurrency jobs.
package digest

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
	"strconv"
)

const (
	// DefaultInterval is the default polling interval. The digest is weekly-grained; a 10-minute round only ensures
	// "a new week started" and "the quiet window ended" are picked up within minutes, and the DB cost degrades to one candidate query.
	DefaultInterval = worker.DefaultInterval
	// DefaultStatsPath is the stats page path (the review link in the digest body).
	DefaultStatsPath = "/stats"
	// runFailedLog is the English log message for a failed round, injected as the shared skeleton worker.Config.FailMessage;
	// the copy is identical to before the refactor.
	runFailedLog = "weekly digest run failed"
)

// Enqueuer is the mail entry point the digest worker depends on; production is satisfied by *mail.Outbox, tests inject a stub.
type Enqueuer interface {
	// Enqueue only enqueues; it does not send mail synchronously.
	Enqueue(ctx context.Context, m mail.Message) error
	// Configured reports whether SMTP is configured.
	Configured() bool
}

// Deps holds the Worker's explicit dependencies.
type Deps struct {
	// DB is required (candidate query and send ledger).
	DB *gorm.DB
	// Outbox is the required mail entry point.
	Outbox Enqueuer
	// Translator is required: mail copy is user-visible copy and always goes through the language pack.
	Translator *i18n.Translator
	// Logger is optional; slog.Default() is used when empty.
	Logger *slog.Logger
	// Now is an injectable clock; system UTC time is used when empty.
	Now func() time.Time
	// Interval is the polling interval; DefaultInterval is used when <=0.
	Interval time.Duration
	// BaseURL is the site root address, used to build the stats page and unsubscribe links; falls back to a relative path when empty.
	BaseURL string
	// StatsPath overrides the stats page path; DefaultStatsPath is used when empty.
	StatsPath string
	// Tokens issues one-click unsubscribe tokens; the mail carries no unsubscribe header when empty.
	Tokens *auth.ActionTokenService
	// Templates fetches the administrator-defined mail templates; empty means only the built-in body is used.
	Templates mail.LookupFunc
	// SiteDefaultLocale is the site default language, the second level of the template fallback chain; that level is skipped when empty.
	SiteDefaultLocale func() string
}

// Worker holds the background worker and its configuration.
//
// Start/stop and the tick loop come from the embedded shared skeleton *worker.Lifecycle (internal/worker): Start/Stop
// are promoted by it, the signatures match the pre-refactor ones, and the calls in cmd/engram are unchanged.
type Worker struct {
	*worker.Lifecycle

	db         *gorm.DB
	outbox     Enqueuer
	translator *i18n.Translator
	logger     *slog.Logger
	now        func() time.Time
	baseURL    string
	statsPath  string
	tokens     *auth.ActionTokenService
	// templates fetches custom mail templates; when missing, the built-in body is used.
	templates mail.LookupFunc
	// siteLocale provides the site default language (second level of the fallback chain); may be nil.
	siteLocale func() string
}

// New constructs a Worker; it does not start the worker, call Start afterwards.
func New(deps Deps) (*Worker, error) {
	if deps.DB == nil {
		return nil, errors.New("digest: Deps.DB is required")
	}
	if deps.Outbox == nil {
		return nil, errors.New("digest: Deps.Outbox is required")
	}
	if deps.Translator == nil {
		return nil, errors.New("digest: Deps.Translator is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	path := strings.TrimSpace(deps.StatsPath)
	if path == "" {
		path = DefaultStatsPath
	}
	w := &Worker{
		db:         deps.DB,
		outbox:     deps.Outbox,
		translator: deps.Translator,
		logger:     logger,
		now:        now,
		baseURL:    strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		statsPath:  path,
		tokens:     deps.Tokens,
		templates:  deps.Templates,
		siteLocale: deps.SiteDefaultLocale,
	}
	// Start/stop and the tick loop are delegated to the shared skeleton; RunOnce is injected as the business phase, and the failure log copy is kept verbatim.
	lifecycle, err := worker.New(worker.Config{
		RunOnce:     w.RunOnce,
		Logger:      logger,
		FailMessage: runFailedLog,
		Interval:    deps.Interval,
	})
	if err != nil {
		return nil, err
	}
	w.Lifecycle = lifecycle
	return w, nil
}

// RunOnce runs one scan round: for each active user who can receive mail, decide whether the weekly digest should be sent.
// Exported for tests and manual operations triggering; a single user's failure is logged and processing continues with the rest.
func (w *Worker) RunOnce(ctx context.Context) error {
	if !w.outbox.Configured() {
		w.logger.Info("weekly digest skipped: smtp not configured")
		return nil
	}
	now := w.now().UTC()
	candidates, err := store.DigestCandidates(ctx, w.db)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.maybeSend(ctx, c, now); err != nil {
			w.logger.Error("weekly digest failed", "user_id", c.ID, "error", err)
		}
	}
	return nil
}

// MinDigestInterval only catches hour-level collisions at the week boundary; it does not act as a one-week cooldown, so a
// backfill several days after downtime is not mistakenly blocked.
const MinDigestInterval = 20 * time.Hour

// maybeSend decides and (when the conditions are met) enqueues one digest.
//
// Decision order (every gate must pass before enqueueing):
//  1. Send hour: if the user's local time has not yet reached their chosen hour (default 19:00, shared with the review reminder),
//     skip the whole round and **do not record the ledger** — the next round after that hour re-evaluates and sends.
//  2. Weekly cap: do not send again once it has been sent for the user's local week start (Monday).
//  3. Preference gate: C-class defaults to off, and turning it off takes effect immediately (preferences are re-read every round, not cached).
//
// The ledger is written only after Enqueue succeeds, so a failed enqueue does not consume the current week's quota.
func (w *Worker) maybeSend(ctx context.Context, c store.DigestCandidate, now time.Time) error {
	loc := reviewday.Location(c.Timezone)
	if !reminder.ReachedSendHour(now.In(loc), reminder.SendHour(c.ReminderHour)) {
		return nil
	}
	// When blocked by the interval gate, the weekly ledger is not written, so it can still be backfilled once the time is reached.
	if last, ok, err := store.LastDigestSentAt(ctx, w.db, c.ID); err != nil {
		return err
	} else if ok && now.Sub(last) < MinDigestInterval {
		return nil
	}
	cutoff := reviewday.ResolveCutoff(c.DayCutoffHour)
	day := reviewday.Day(now, loc, cutoff)
	weekStart := weekStartOf(day)

	sent, err := store.HasDigestBeenSent(ctx, w.db, c.ID, weekStart)
	if err != nil {
		return err
	}
	if sent {
		return nil
	}

	choices, err := store.NewEmailPrefStore(w.db).Choices(ctx, c.ID)
	if err != nil {
		return err
	}
	if !mail.ResolveEnabled(choices, mail.TypeStudyDigest) {
		return nil
	}

	stats, err := ComputeStats(ctx, w.db, c.ID, now, loc, cutoff)
	if err != nil {
		return err
	}
	if err := w.outbox.Enqueue(ctx, w.message(ctx, c, stats)); err != nil {
		return err
	}
	if err := store.RecordDigestSent(ctx, w.db, c.ID, weekStart, now); err != nil {
		return err
	}
	w.logger.Info("weekly digest enqueued", "user_id", c.ID, "week_start", weekStart, "reviews", stats.Reviewed)
	return nil
}

// DigestStats holds the numbers shown in one weekly digest. Each field comes directly from a StatsStore aggregate method.
type DigestStats struct {
	// WindowFrom / WindowTo are the review-count window (review_day inclusive), matching the stats page's "last 7 days".
	WindowFrom string
	WindowTo   string
	// Reviewed is the number of reviews within the window (= ReviewVolume.Last7Days).
	Reviewed int64
	// PassRate is the pass rate / retention rate on the same basis as the stats page (= RetentionByStability.Rate, 0–1).
	PassRate float64
	// StreakCurrent is the unbroken streak of review days up to "today" (= Streak.Current).
	StreakCurrent int
	// NewCards is the number of newly introduced cards within the window (the sum of LearningCurve's New).
	NewCards int64
	// DueNow is the number of cards the user's review queue serves right now (= schedule.QueueBuilder.DueNow), the same
	// number as the home page and the reminder mail.
	DueNow int64
}

// ComputeStats uses the stats service to compute the digest numbers. This is the single source of digest numbers: it calls
// the same batch of StatsStore methods and the same review-day window as the stats JSON endpoint, so the two cannot diverge.
func ComputeStats(ctx context.Context, db *gorm.DB, userID uint64, now time.Time, loc *time.Location, cutoffHour int) (DigestStats, error) {
	today := reviewday.Day(now, loc, cutoffHour)
	from := reviewday.Shift(today, -6)

	stats := store.NewStatsStore(db)
	volume, err := stats.ReviewVolume(ctx, userID, today)
	if err != nil {
		return DigestStats{}, err
	}
	retention, err := stats.RetentionByStability(ctx, userID, 0)
	if err != nil {
		return DigestStats{}, err
	}
	streak, err := stats.Streak(ctx, userID, now, loc, cutoffHour)
	if err != nil {
		return DigestStats{}, err
	}
	curve, err := stats.LearningCurve(ctx, userID, from, today)
	if err != nil {
		return DigestStats{}, err
	}
	due, err := schedule.NewQueueBuilder(db, store.NewDeckStore(db), nil).DueNow(ctx, userID, schedule.QueueOptions{
		Now: now, Location: loc, DayCutoffHour: &cutoffHour,
	})
	if err != nil {
		return DigestStats{}, err
	}

	var newCards int64
	for _, p := range curve {
		newCards += p.New
	}
	return DigestStats{
		WindowFrom:    from,
		WindowTo:      today,
		Reviewed:      volume.Last7Days,
		PassRate:      retention.Rate,
		StreakCurrent: streak.Current,
		NewCards:      newCards,
		DueNow:        due,
	}, nil
}

// message assembles the digest mail in the user's language. The digest is a C-class optional mail and therefore carries the
// RFC 8058 one-click unsubscribe header: the token names the single type study_digest, so the recipient can turn it off without
// signing in. A failed token issue only logs and the mail is still sent.
func (w *Worker) message(ctx context.Context, c store.DigestCandidate, s DigestStats) mail.Message {
	lc := w.translator.Localizer(w.translator.Pick("", c.Locale, ""))
	site := lc.T("app.name")
	statsURL := w.statsURL()
	unsubURL := w.unsubscribeLink(ctx, c.ID)
	// All six stat lines are in the built-in body (mail.DefaultTemplate), so the new-cards/due numbers must go into
	// the variable table — passing them only to the render function leaves no variable behind, the admin page cannot see them, and templates cannot use them.
	vars := mail.Vars{
		"site":      site,
		"count":     strconv.FormatInt(s.Reviewed, 10),
		"rate":      store.FormatPercent(s.PassRate),
		"days":      strconv.Itoa(s.StreakCurrent),
		"new_cards": strconv.FormatInt(s.NewCards, 10),
		"due":       strconv.FormatInt(s.DueNow, 10),
		"url":       statsURL,
	}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	footerNote, unsubLabel := lc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = lc.T("mail.footer.note")
		unsubLabel = lc.T("mail.footer.unsubscribe")
	}
	fbSubject, fallbackBodyMD, _ := mail.DefaultTemplate(mail.TypeStudyDigest, mail.VariantDefault, lc.Tf, vars)
	tpl := mail.Resolve(w.templates, mail.TypeStudyDigest, lc.Locale(), w.siteDefaultLocale())
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mail.TypeStudyDigest,
		Site:             site,
		Lang:             lc.Locale(),
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fbSubject,
		FallbackBodyMD:   fallbackBodyMD,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{
		To:       c.Email,
		Type:     string(mail.TypeStudyDigest),
		Subject:  out.Subject,
		TextBody: out.Text,
		HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeStudyDigest, unsubURL)
	return msg
}

// siteDefaultLocale returns the site default language (second level of the template fallback chain); returns an empty string when not injected.
func (w *Worker) siteDefaultLocale() string {
	if w.siteLocale == nil {
		return ""
	}
	return strings.TrimSpace(w.siteLocale())
}

// unsubscribeLink issues an unsubscribe token for study_digest and returns the unsubscribe link; returns an empty string when unavailable.
//
// It returns a link rather than a header: the body footer and the plain-text part need it too (headers are only visible to mail clients).
func (w *Worker) unsubscribeLink(ctx context.Context, userID uint64) string {
	if w.tokens == nil {
		return ""
	}
	token, err := w.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(mail.TypeStudyDigest), auth.UnsubscribeTTL)
	if err != nil {
		w.logger.Error("weekly digest: issue unsubscribe token failed", "user_id", userID, "error", err)
		return ""
	}
	return w.baseURL + "/unsubscribe?token=" + url.QueryEscape(token)
}

// statsURL builds the stats page link; falls back to a relative path when BaseURL is not configured.
func (w *Worker) statsURL() string {
	return w.baseURL + w.statsPath
}

// weekStartOf returns the Monday of the week containing day (YYYY-MM-DD). Go's Weekday uses Sunday as 0, so this converts to
// "days since this week's Monday" and subtracts it, guaranteeing local Monday early morning and Sunday of the same week fall on the same week_start.
func weekStartOf(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	offset := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -offset).Format("2006-01-02")
}

// Compile-time confirmation that *mail.Outbox satisfies Enqueuer (so contract drift breaks here, not at the assembly site).
var _ Enqueuer = (*mail.Outbox)(nil)
