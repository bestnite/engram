package mail

import (
	"context"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the inbound delivery helper for class D (admin notification mail), mirroring
// SecurityNotifier in security.go: the three hard constraints are gathered in one place so that the
// callers (the individual trigger points) do not each have to implement them.
//
//   - Never send synchronously: it only calls Outbox.Enqueue to write the queue table;
//   - A send failure must not fail the triggering operation -- Send returns no error; when SMTP is not
//     configured or the enqueue fails it returns false and logs in English, never propagating upward
//     (registration, job failure and quota rejection must never fail because of mail);
//   - Class D can be disabled: decided by the **receiving admin's own** preference, and nothing is
//     sent when it is off; the catalog decision goes through ResolveEnabled, so "the preferences page
//     can turn it off while the backend keeps sending" is impossible.
//
// The unsubscribe header is a separate task; no Headers are written here.
//
// Naming boundary: this file must not define Message, Outbox, ErrNotConfigured, Configured -- those are
// transport-layer symbols, and a duplicate declaration would collide.

// AdminNotifier writes class D admin notifications into the outbox.
type AdminNotifier struct {
	outbox *Outbox
	prefs  *store.EmailPrefStore
	logger *slog.Logger
}

// NewAdminNotifier builds the class D delivery helper; prefs may be nil (then only the catalog default
// applies, with no per-user gate).
func NewAdminNotifier(outbox *Outbox, prefs *store.EmailPrefStore, logger *slog.Logger) *AdminNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminNotifier{outbox: outbox, prefs: prefs, logger: logger}
}

// Configured reports whether SMTP is configured; the trigger points use it to decide whether to render
// the mail.not_configured explanation.
func (n *AdminNotifier) Configured() bool {
	return n != nil && n.outbox != nil && n.outbox.Configured()
}

// Send writes one class D mail into the outbox and returns whether it was enqueued.
//
// It deliberately returns no error: triggering operations such as registration, job failure or media
// quota rejection must never fail because enqueuing mail reported an error. Unconfigured SMTP, an
// unregistered type, the receiving admin having turned the class off and an enqueue failure all just
// log in English and return false.
func (n *AdminNotifier) Send(ctx context.Context, adminID uint64, to string, t Type, subject, textBody, htmlBody string) bool {
	if !n.Configured() {
		return false
	}
	if _, ok := Lookup(t); !ok {
		n.logger.Error("mail: unknown admin mail type", "admin_id", adminID, "type", string(t))
		return false
	}
	// Class D can be disabled: respect the receiving admin's explicit choice for each class D type; when
	// reading it fails, fall back to the catalog default (on) and carry on, but never silently -- log it.
	if n.prefs != nil {
		choices, err := n.prefs.Choices(ctx, adminID)
		if err != nil {
			n.logger.Error("mail: load admin email preferences failed", "admin_id", adminID, "type", string(t), "error", err)
		} else if !ResolveEnabled(choices, t) {
			return false
		}
	}
	// Headers are left empty on this path: AdminNotifier has no token service, so class-D mail sent through it carries no one-click
	// unsubscribe link — the preference switch above is the only way for an admin to turn the class off.
	if err := n.outbox.Enqueue(ctx, Message{
		To:       to,
		Type:     string(t),
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}); err != nil {
		n.logger.Error("mail: enqueue admin notification failed",
			"admin_id", adminID, "type", string(t), "error", err)
		return false
	}
	return true
}
