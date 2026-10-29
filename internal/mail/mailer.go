package mail

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the single path for an outgoing mail. Every sender (the web layer, the review reminder, the weekly digest)
// supplies only the type, the recipient and the variable values; whether SMTP is ready, whether the recipient wants the
// type, the site name, the template lookup, the unsubscribe link, the footer and the headers are all decided here, so two
// senders cannot gate or render the same kind of mail differently.

// UnsubscribeTTL is the validity of a one-click unsubscribe link. Unsubscribe links stay in old mail for a long time and a
// user may only think of clicking months later, so it is generous; the token is still one-time and expiring.
const UnsubscribeTTL = 90 * 24 * time.Hour

// Localizer is the part of a request or recipient localizer the mailer needs. Copy shown to the recipient comes from
// the language packs through it; this package does not import i18n.
type Localizer interface {
	T(id string) string
	Tf(id string, data map[string]any) string
	Locale() string
}

// TokenIssuer issues one-click unsubscribe tokens; *auth.ActionTokenService satisfies it.
type TokenIssuer interface {
	Issue(ctx context.Context, userID uint64, purpose, payload string, ttl time.Duration) (string, error)
}

// Footer returns the footer note and the unsubscribe label for a mail: the opt-out note and the link label when unsubURL
// is set, the "sent automatically" note otherwise. Every mail carries one of the two.
func Footer(t func(id string) string, unsubURL string) (note, label string) {
	if unsubURL == "" {
		return t("mail.footer.automated"), ""
	}
	return t("mail.footer.note"), t("mail.footer.unsubscribe")
}

// Enqueuer is where a composed mail goes; *Outbox satisfies it, tests inject a recorder.
type Enqueuer interface {
	// Enqueue only enqueues; delivery happens on the outbox worker.
	Enqueue(ctx context.Context, m Message) error
	// Configured reports whether SMTP is configured.
	Configured() bool
}

// The compiler confirms *Outbox satisfies Enqueuer, so contract drift errors here rather than at a wiring site.
var _ Enqueuer = (*Outbox)(nil)

// MailerDeps are the explicit dependencies of a Mailer.
type MailerDeps struct {
	// DB holds the settings table (the admin-configured site name and default locale) and the email preferences. Required.
	DB *gorm.DB
	// Outbox receives composed mail; nil means mail is not configured.
	Outbox Enqueuer
	// Templates looks up admin-customised templates; nil means built-in bodies only.
	Templates LookupFunc
	// Tokens issues unsubscribe tokens; nil sends optional mail without an unsubscribe link.
	Tokens TokenIssuer
	// BaseURL is the configured site root. Absolute links in a mail are built from it.
	BaseURL string
	// Locales are the supported locale codes; a configured site default locale outside this list is ignored.
	Locales []string
	// Logger is optional; slog.Default() is used when nil.
	Logger *slog.Logger
}

// Mailer composes and enqueues outgoing mail. One instance is shared by every sender in the process.
type Mailer struct {
	db        *gorm.DB
	outbox    Enqueuer
	templates LookupFunc
	tokens    TokenIssuer
	baseURL   string
	locales   []string
	logger    *slog.Logger
}

// NewMailer builds a Mailer.
func NewMailer(d MailerDeps) *Mailer {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Mailer{
		db:        d.DB,
		outbox:    d.Outbox,
		templates: d.Templates,
		tokens:    d.Tokens,
		baseURL:   strings.TrimRight(strings.TrimSpace(d.BaseURL), "/"),
		locales:   d.Locales,
		logger:    logger,
	}
}

// Mail describes one mail to compose.
type Mail struct {
	Type Type
	// Variant selects an alternative built-in body of the same type; empty means VariantDefault.
	Variant string
	To      string
	// UserID is the recipient's account. For an optional type it receives the unsubscribe token; 0 means the recipient
	// has no account (an invite to an outside address) and the mail carries no unsubscribe link.
	UserID uint64
	// Vars are the variable values; "site" and "unsubscribe_url" are filled in by the mailer.
	Vars Vars
	// BaseURL overrides the configured base for the unsubscribe link, for a sender that derived the site root from the
	// current request because none is configured. Empty uses the configured base.
	BaseURL string
}

// Compose renders m in the recipient's language: the admin template when one exists for the type, the built-in body
// otherwise, with the footer and, for an optional type, the unsubscribe link and RFC 8058 headers.
func (c *Mailer) Compose(ctx context.Context, lc Localizer, m Mail) Message {
	vars := make(Vars, len(m.Vars)+2)
	for k, v := range m.Vars {
		vars[k] = v
	}
	site := c.SiteName(ctx, lc)
	vars["site"] = site
	unsubURL := c.UnsubscribeLink(ctx, m)
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	variant := m.Variant
	if variant == "" {
		variant = VariantDefault
	}
	fbSubject, fbBody, _ := DefaultTemplate(m.Type, variant, lc.Tf, vars)
	tpl := Resolve(c.templates, m.Type, lc.Locale(), c.SiteDefaultLocale(ctx))
	note, label := Footer(lc.T, unsubURL)
	out := RenderOrFallback(RenderInput{
		Type:             m.Type,
		Site:             site,
		Lang:             lc.Locale(),
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fbSubject,
		FallbackBodyMD:   fbBody,
		UnsubscribeURL:   unsubURL,
		FooterNote:       note,
		UnsubscribeLabel: label,
	})
	return Message{
		To:       m.To,
		Type:     string(m.Type),
		Subject:  out.Subject,
		TextBody: out.Text,
		HTMLBody: out.HTML,
		Headers:  UnsubscribeHeaders(m.Type, unsubURL),
	}
}

// SendResult is the outcome of Send.
type SendResult int

const (
	// SendQueued means the mail is in the outbox.
	SendQueued SendResult = iota
	// SendNotConfigured means SMTP is not configured, so nothing was queued.
	SendNotConfigured
	// SendOptedOut means the recipient turned the type off (or never turned on a type that defaults to off).
	SendOptedOut
	// SendFailed means the type is unknown or enqueueing failed; the cause is logged.
	SendFailed
)

// Configured reports whether mail can be sent at all (an outbox is wired and SMTP is configured).
func (c *Mailer) Configured() bool {
	return c.outbox != nil && c.outbox.Configured()
}

// Send composes m and enqueues it when SMTP is configured and the recipient wants the type. It returns no error on
// purpose: the action that triggered a mail (a login, a share, a finished job) must never fail because of the mail, so
// every failure is logged here and reported only as a SendResult.
//
// The preference read is the recipient's own choice when m.UserID names an account; a recipient without an account gets
// the type's default. When reading the preferences fails, the type's default applies too, and the failure is logged.
func (c *Mailer) Send(ctx context.Context, lc Localizer, m Mail) SendResult {
	if !c.Configured() {
		c.logger.Info("mail: smtp is not configured; mail not sent", "mail_type", string(m.Type))
		return SendNotConfigured
	}
	if _, ok := Lookup(m.Type); !ok {
		c.logger.Error("mail: unknown mail type", "mail_type", string(m.Type), "user_id", m.UserID)
		return SendFailed
	}
	enabled, err := c.Enabled(ctx, m.UserID, m.Type)
	if err != nil {
		c.logger.Error("mail: load email preferences failed", "user_id", m.UserID, "mail_type", string(m.Type), "error", err)
		enabled = ResolveEnabled(nil, m.Type)
	}
	if !enabled {
		return SendOptedOut
	}
	if err := c.outbox.Enqueue(ctx, c.Compose(ctx, lc, m)); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return SendNotConfigured
		}
		c.logger.Error("mail: enqueue failed", "user_id", m.UserID, "mail_type", string(m.Type), "error", err)
		return SendFailed
	}
	return SendQueued
}

// Enabled reports whether the user receives mail of type t: the user's own choice when they made one, the type's
// default otherwise. Preferences are read on every call so turning a type off takes effect immediately.
func (c *Mailer) Enabled(ctx context.Context, userID uint64, t Type) (bool, error) {
	if userID == 0 {
		return ResolveEnabled(nil, t), nil
	}
	choices, err := store.NewEmailPrefStore(c.db).Choices(ctx, userID)
	if err != nil {
		return false, err
	}
	return ResolveEnabled(choices, t), nil
}

// SiteName is the name a mail is signed with: the admin-configured site name, or the language pack's app name.
func (c *Mailer) SiteName(ctx context.Context, lc Localizer) string {
	if name := c.setting(ctx, store.SettingSiteName); name != "" {
		return name
	}
	return lc.T("app.name")
}

// SiteDefaultLocale is the admin-configured default locale when it is a supported one, otherwise empty. It is the second
// level of the template fallback chain and the last level of request language resolution.
func (c *Mailer) SiteDefaultLocale(ctx context.Context) string {
	raw := c.setting(ctx, store.SettingSiteDefaultLocale)
	for _, code := range c.locales {
		if code == raw {
			return raw
		}
	}
	return ""
}

// URL joins path onto base, or onto the configured base when base is empty. The result is relative when neither is set.
func (c *Mailer) URL(base, path string) string {
	if base = strings.TrimRight(strings.TrimSpace(base), "/"); base == "" {
		base = c.baseURL
	}
	return base + path
}

// UnsubscribeLink issues the one-click unsubscribe link for an optional type sent to an account. It returns empty for a
// type that cannot be turned off, for a recipient without an account, when no token service is wired, when no absolute
// site root is known (a relative link in a mail does not open), and when issuing fails: a missing unsubscribe entry must
// never stop the mail itself.
func (c *Mailer) UnsubscribeLink(ctx context.Context, m Mail) string {
	if !CanUnsubscribe(m.Type) || m.UserID == 0 || c.tokens == nil {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(m.BaseURL), "/")
	if base == "" {
		base = c.baseURL
	}
	if base == "" {
		c.logger.Info("mail: no base url is configured; unsubscribe link omitted", "mail_type", string(m.Type))
		return ""
	}
	token, err := c.tokens.Issue(ctx, m.UserID, store.ActionTokenUnsubscribe, string(m.Type), UnsubscribeTTL)
	if err != nil {
		c.logger.Error("mail: issue unsubscribe token failed", "user_id", m.UserID, "mail_type", string(m.Type), "error", err)
		return ""
	}
	return base + "/unsubscribe?token=" + url.QueryEscape(token)
}

// setting reads one trimmed value from the settings table; a read failure reads as unset.
func (c *Mailer) setting(ctx context.Context, key string) string {
	if c.db == nil {
		return ""
	}
	settings, err := store.LoadSettings(ctx, c.db)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(settings[key])
}
