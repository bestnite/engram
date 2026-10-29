// Package reminder implements the class-C "review due reminder" periodic background worker.
//
// Behavioral constraints:
//   - Never send mail synchronously in the request path: always Enqueue, delivery is the outbox worker's job.
//   - Send time follows **the user's local time**: each user may pick their own top-of-hour hour (users.reminder_hour, 0–23),
//     falling back to the global default DefaultSendHour (19:00) when unset. The first poll after that hour arrives sends;
//     the hour is interpreted in the user's timezone (see SendHour / reachedSendHour).
//   - At most one per day: decided by the user's local review day (same timezone / cutoff semantics as reviews.review_day),
//     ledger in store.ReminderLog.
//   - When SMTP is unconfigured, send nothing and report no error, just log one English line (class C is optional operational mail; no page needs disabling).
//
// Why the old quiet window (local 23:00–07:00) was removed: the sole purpose of that heuristic was to move "send once past the day cutoff"
// reminders out of the late night, and it was exactly the root cause of "everyone gets nagged at 7am". Send time is now explicitly chosen by the user
// (default 19:00, already outside the window), so the quiet window serves no purpose and would in turn suppress a late-night slot the user explicitly
// picked — an explicit choice wins, so the whole heuristic was deleted together with InQuietHours, and its test cases were removed along with it.
//
// Why a periodic worker rather than a jobs table: the optimiser's single-concurrency constraint does not apply to mail,
// and stuffing reminders into jobs would block parameter optimisation and vice versa. A reminder is not a long task, just a periodic "due scan",
// so it gets its own lightweight worker, started with serve and gracefully stopped on signal.
//
// "Event-driven" here lands as: every round scans the event "are there due cards", and sends only if there are; no due cards, no send.
package reminder

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
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
	"strconv"
)

const (
	// DefaultInterval is the default poll interval.
	//
	// Why 10 minutes: a reminder is a nudge, not a punctual event, so users cannot perceive minute-level differences;
	// send time is hour-grained and the daily cap is review-day-grained, so 10 minutes is enough to follow up soon after the user's chosen hour arrives
	// while keeping each round's DB cost at the level of "one candidate query + one or two small queries per user".
	DefaultInterval = worker.DefaultInterval
	// DefaultSendHour is the global default (local 19:00) when no per-user send hour is set.
	//
	// Why 19:00: the reminder is the nudge "you still have cards to review today", and early evening is when most people can sit down to review,
	// and it also lies outside the old quiet window (23:00–07:00). The weekly digest reuses the same hour, so both class-C mails arrive at the same time of day.
	DefaultSendHour = 19
	// MinSendInterval is the minimum interval between two review reminders for the same user (takes effect across review days).
	//
	// Why it is needed: send time is determined by the user's chosen hour, but the ledger counts by review day. If the user picks an hour earlier than their
	// day cutoff (e.g. picks 0, cutoff 4), the tail of the previous review day (00:00) and the head of the new review day (04:00) fall on the same calendar
	// day only a few hours apart — "at most one per review day" cannot by definition stop such back-to-back double sends.
	//
	// Why 20 hours: the normal rhythm between adjacent review days is 24 hours (23 under DST), and 20 hours sits below that
	// leaving at least 3 hours of margin, so it will not suppress genuine next-day reminders; whereas the interval of a back-to-back double send equals "day cutoff −
	// chosen hour", which is on the order of a few hours (0 and 4 are only 4 hours apart), far below 20 hours, so it is always caught. It is also far below a week,
	// so it does not tangle with the weekly digest's weekly rhythm. Rounding to "hour level" matches the granularity of send time and keeps mental arithmetic easy.
	//
	// Boundary: the closer "day cutoff − chosen hour" gets to a full day, the less it looks like a back-to-back double send (in the extreme, choosing 0 with cutoff 23,
	// the two mails are already about a day apart, indistinguishable from the normal next-day rhythm), and this gate does not block it — blocking it would require
	// raising the threshold above 24 hours, which would in turn suppress normal daily reminders. 20 hours is therefore the trade-off point between "catching
	MinSendInterval = 20 * time.Hour
	// DefaultReviewPath is the review page path (the link in the reminder body).
	DefaultReviewPath = "/review"
	// runFailedLog is the English log message for a single failed round, injected into the shared skeleton worker.Config.FailMessage;
	// the wording is verbatim identical to before the refactor.
	runFailedLog = "review reminder run failed"
)

// Enqueuer is the send entry point the reminder worker depends on; in production it is satisfied by *mail.Outbox, tests inject a stand-in.
type Enqueuer interface {
	// Enqueue only enqueues; it does not send synchronously.
	Enqueue(ctx context.Context, m mail.Message) error
	// Configured reports whether SMTP is configured.
	Configured() bool
}

// Deps are the explicit dependencies of Reminder.
type Deps struct {
	// DB is required (candidate query and send ledger).
	DB *gorm.DB
	// Outbox is the required send entry point.
	Outbox Enqueuer
	// Translator is required: mail copy is user-visible copy and always goes through the language catalogs.
	Translator *i18n.Translator
	// Logger is optional; slog.Default() is used when nil.
	Logger *slog.Logger
	// Now is an injectable clock; the system UTC time is used when nil. Tests for the send hour and the daily cap rely on it for control.
	Now func() time.Time
	// Interval is the poll interval; DefaultInterval when <=0.
	Interval time.Duration
	// BaseURL is the site root, used to build the review page link; when empty the link is a relative path.
	BaseURL string
	// ReviewPath overrides the review page path; DefaultReviewPath when empty.
	ReviewPath string
	// Tokens issues one-click unsubscribe tokens; when nil the reminder mail carries no unsubscribe header (only optional types need it).
	Tokens *auth.ActionTokenService
	// Templates fetches admin-customised mail templates; nil means built-in bodies only.
	Templates mail.LookupFunc
	// SiteDefaultLocale is the site default locale, the second level of the template fallback chain; that level is skipped when empty.
	SiteDefaultLocale func() string
}

// Reminder holds the background worker and its configuration.
//
// Start/stop and the tick loop come from the embedded shared skeleton *worker.Lifecycle (internal/worker): Start/Stop
// are promoted from it, with signatures identical to before the refactor, so the calls in cmd/engram are unchanged.
type Reminder struct {
	*worker.Lifecycle

	db         *gorm.DB
	outbox     Enqueuer
	translator *i18n.Translator
	logger     *slog.Logger
	now        func() time.Time
	baseURL    string
	reviewPath string
	// tokens issues unsubscribe tokens; no unsubscribe header when nil.
	tokens *auth.ActionTokenService
	// templates fetches custom mail templates; built-in bodies are used when absent.
	templates mail.LookupFunc
	// siteLocale provides the site default locale (second level of the fallback chain); may be nil.
	siteLocale func() string
}

// New constructs a Reminder; it does not start the worker, Start must be called afterwards.
func New(deps Deps) (*Reminder, error) {
	if deps.DB == nil {
		return nil, errors.New("reminder: Deps.DB is required")
	}
	if deps.Outbox == nil {
		return nil, errors.New("reminder: Deps.Outbox is required")
	}
	if deps.Translator == nil {
		return nil, errors.New("reminder: Deps.Translator is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	path := strings.TrimSpace(deps.ReviewPath)
	if path == "" {
		path = DefaultReviewPath
	}
	r := &Reminder{
		db:         deps.DB,
		outbox:     deps.Outbox,
		translator: deps.Translator,
		logger:     logger,
		now:        now,
		baseURL:    strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		reviewPath: path,
		tokens:     deps.Tokens,
		templates:  deps.Templates,
		siteLocale: deps.SiteDefaultLocale,
	}
	// Start/stop and the tick loop are handed to the shared skeleton; RunOnce is injected as the business phase, and the failure log wording is kept verbatim.
	lifecycle, err := worker.New(worker.Config{
		RunOnce:     r.RunOnce,
		Logger:      logger,
		FailMessage: runFailedLog,
		Interval:    deps.Interval,
	})
	if err != nil {
		return nil, err
	}
	r.Lifecycle = lifecycle
	return r, nil
}

// RunOnce runs one due scan: for every active user with due cards, decide whether a reminder should be sent.
// Exported so tests and operators can trigger it manually; the returned error only reflects query/enqueue-level failures, a single user's failure
// is logged and the remaining users are still processed.
func (r *Reminder) RunOnce(ctx context.Context) error {
	if !r.outbox.Configured() {
		// Class C is optional operational mail: skip silently when SMTP is unconfigured, no error, but leave an English log line.
		r.logger.Info("review reminder skipped: smtp not configured")
		return nil
	}
	now := r.now().UTC()
	candidates, err := store.ReminderCandidates(ctx, r.db, now)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.maybeSend(ctx, c, now); err != nil {
			r.logger.Error("review reminder failed", "user_id", c.ID, "error", err)
		}
	}
	return nil
}

// maybeSend decides and (when the conditions hold) enqueues one reminder.
//
// Decision order (every gate must pass before enqueueing):
//  1. Send time: if the user's local time has not reached their chosen hour (default 19:00), skip the whole round and **do not record in the ledger** —
//     the next round after that hour arrives re-evaluates and sends, i.e. "send once the hour is due".
//  2. Minimum interval: if less than MinSendInterval has passed since the last successful send (max SentAt across review days), skip the whole round
//     and **do not record in the ledger** — the send can still follow up once the interval passes. This gate specifically handles the same calendar day
//     back-to-back double send when "the chosen hour precedes the day cutoff" (the daily ledger counts by review day and cannot stop it).
//  3. Daily cap: if the user's local review day already had a send, do not send again.
//  4. Preference gate: class C defaults to off, and the user turning it off takes effect immediately (preferences are read fresh every round, not cached).
//
// The ledger is written only after Enqueue succeeds, so a failed enqueue does not consume that day's quota.
//
// Hour vs review-day boundary: send time is compared against the user's local hour (local.Hour() >= chosen hour), while the review day is still split by
// day_cutoff_hour. If the chosen hour precedes the day cutoff (e.g. choose 0, cutoff 4), that instant still belongs to
// the previous review day, so 0 and 4 in the same calendar day each hit a different review day; the ledger cannot help here, so the
// second minimum-interval gate above (MinSendInterval) catches it, see that constant's comment.
func (r *Reminder) maybeSend(ctx context.Context, c store.ReminderCandidate, now time.Time) error {
	loc := reviewday.Location(c.Timezone)
	if !ReachedSendHour(now.In(loc), SendHour(c.ReminderHour)) {
		return nil
	}
	if last, ok, err := store.LastReminderSentAt(ctx, r.db, c.ID); err != nil {
		return err
	} else if ok && now.Sub(last) < MinSendInterval {
		return nil
	}
	cutoff := reviewday.ResolveCutoff(c.DayCutoffHour)
	day := reviewday.Day(now, loc, cutoff)

	sent, err := store.HasReminderBeenSent(ctx, r.db, c.ID, day)
	if err != nil {
		return err
	}
	if sent {
		return nil
	}

	choices, err := store.NewEmailPrefStore(r.db).Choices(ctx, c.ID)
	if err != nil {
		return err
	}
	if !mail.ResolveEnabled(choices, mail.TypeReviewReminder) {
		return nil
	}

	if err := r.outbox.Enqueue(ctx, r.message(ctx, c)); err != nil {
		return err
	}
	if err := store.RecordReminderSent(ctx, r.db, c.ID, day, now); err != nil {
		return err
	}
	r.logger.Info("review reminder enqueued", "user_id", c.ID, "review_day", day, "due_count", c.DueCount)
	return nil
}

// message assembles the reminder mail in the user's language. A reminder is class-C optional mail, so it carries the RFC 8058 one-click unsubscribe header:
// the token names the single type review_reminder, and the recipient (necessarily a user of this site) can turn it off without logging in.
// A failed token issuance is only logged and the mail is still sent: a missing unsubscribe entry must not stop the reminder from going out.
func (r *Reminder) message(ctx context.Context, c store.ReminderCandidate) mail.Message {
	lc := r.translator.Localizer(r.translator.Pick("", c.Locale, ""))
	site := lc.T("app.name")
	reviewURL := r.reviewURL()
	unsubURL := r.unsubscribeLink(ctx, c.ID)

	vars := mail.Vars{"site": site, "count": strconv.FormatInt(c.DueCount, 10), "url": reviewURL}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	footerNote, unsubLabel := lc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = lc.T("mail.footer.note")
		unsubLabel = lc.T("mail.footer.unsubscribe")
	}
	fbSubject, fallbackBodyMD, _ := mail.DefaultTemplate(mail.TypeReviewReminder, mail.VariantDefault, lc.Tf, vars)
	tpl := mail.Resolve(r.templates, mail.TypeReviewReminder, lc.Locale(), r.siteDefaultLocale())
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mail.TypeReviewReminder,
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
		Type:     string(mail.TypeReviewReminder),
		Subject:  out.Subject,
		TextBody: out.Text,
		HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeReviewReminder, unsubURL)
	return msg
}

// siteDefaultLocale returns the site default locale (second level of the template fallback chain); returns an empty string when not injected.
func (r *Reminder) siteDefaultLocale() string {
	if r.siteLocale == nil {
		return ""
	}
	return strings.TrimSpace(r.siteLocale())
}

// unsubscribeHeaders issues an unsubscribe token for review_reminder and returns the RFC 8058 headers; returns nil when unavailable.
// unsubscribeLink issues an unsubscribe token for review_reminder and returns the unsubscribe link; returns an empty string when unavailable.
//
// Returns a link rather than a header: the body footer and the plain-text part need it too (headers are only visible to mail clients).
func (r *Reminder) unsubscribeLink(ctx context.Context, userID uint64) string {
	if r.tokens == nil {
		return ""
	}
	token, err := r.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(mail.TypeReviewReminder), auth.UnsubscribeTTL)
	if err != nil {
		r.logger.Error("review reminder: issue unsubscribe token failed", "user_id", userID, "error", err)
		return ""
	}
	return r.baseURL + "/unsubscribe?token=" + url.QueryEscape(token)
}

// reviewURL builds the review page link; degrades to a relative path when BaseURL is unconfigured.
func (r *Reminder) reviewURL() string {
	return r.baseURL + r.reviewPath
}

// SendHour resolves the user's chosen send hour into the hour actually used: nil (unset) or out-of-range values fall back to
// DefaultSendHour. Returns an int rather than a pointer, so callers need no nil check.
//
// 0 is a valid value (midnight): 0 and "unset" must never be conflated — this is exactly why the column is a nullable pointer rather than
// "a plain int + a default value" (see the comment on store.User.ReminderHour).
func SendHour(userHour *int) int {
	if userHour == nil || *userHour < 0 || *userHour > 23 {
		return DefaultSendHour
	}
	return *userHour
}

// ReachedSendHour reports whether local time has reached (or passed) the chosen top-of-hour hour.
//
// Using "greater than or equal" rather than "equal" is so that a send can still follow up after the worker was down and the poll missed the hour:
// the first poll after the chosen hour arrives satisfies the condition, and later polls are held back by the "one per review day" ledger, so nothing repeats.
//
// Exported for reuse by the weekly digest worker: both class-C mails share the same hour, so the decision must be a single implementation.
func ReachedSendHour(local time.Time, hour int) bool {
	return local.Hour() >= hour
}

// Makes the compiler confirm *mail.Outbox satisfies Enqueuer (so contract drift errors here rather than at the wiring site).
var _ Enqueuer = (*mail.Outbox)(nil)
