package mail

import (
	"context"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the inbound delivery helper for class A (transactional security mail).
//
// All three constraints are gathered here so the callers (the web trigger points) do not each have to
// implement them:
//   - Never send synchronously: it only calls Outbox.Enqueue to write the queue table;
//   - A send failure must not fail the triggering operation -- Send returns no error; when SMTP is not
//     configured or the enqueue fails it returns false and logs in English, never propagating upward;
//   - Class A carries no unsubscribe header: Send never writes Headers, so List-Unsubscribe cannot
//     appear.
//
// Configured lets a trigger point decide to "disable itself and say why when SMTP is not configured".

// SecurityNotifier writes class A security/transactional mail into the outbox.
type SecurityNotifier struct {
	outbox *Outbox
	prefs  *store.EmailPrefStore
	logger *slog.Logger
}

// NewSecurityNotifier builds the class A mail delivery helper; prefs may be nil (class A is not
// affected by preferences).
func NewSecurityNotifier(outbox *Outbox, prefs *store.EmailPrefStore, logger *slog.Logger) *SecurityNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &SecurityNotifier{outbox: outbox, prefs: prefs, logger: logger}
}

// Configured reports whether SMTP is configured; the trigger points use it to disable the relevant
// pages/endpoints and say why.
func (n *SecurityNotifier) Configured() bool {
	return n != nil && n.outbox != nil && n.outbox.Configured()
}

// Send writes one class A mail into the outbox and returns whether it was enqueued.
//
// It deliberately returns no error: operations such as a successful login or password change must
// never fail because Enqueue reported an error. Unconfigured SMTP, an unregistered type and an
// enqueue failure all just log in English and return false.
func (n *SecurityNotifier) Send(ctx context.Context, userID uint64, to string, t Type, subject, textBody, htmlBody string) bool {
	if !n.Configured() {
		return false
	}
	if _, ok := Lookup(t); !ok {
		n.logger.Error("mail: unknown security mail type", "user_id", userID, "type", string(t))
		return false
	}
	// Class A is always true; the catalog decision still runs here so that a disagreement like "the
	// page can turn it off while the backend keeps sending" is impossible.
	if n.prefs != nil {
		choices, err := n.prefs.Choices(ctx, userID)
		if err != nil {
			n.logger.Error("mail: load email preferences failed", "user_id", userID, "type", string(t), "error", err)
		} else if !ResolveEnabled(choices, t) {
			return false
		}
	}
	// Headers left empty: class A mail carries no unsubscribe header.
	if err := n.outbox.Enqueue(ctx, Message{
		To:       to,
		Type:     string(t),
		Subject:  subject,
		TextBody: textBody,
		HTMLBody: htmlBody,
	}); err != nil {
		n.logger.Error("mail: enqueue security notification failed",
			"user_id", userID, "type", string(t), "error", err)
		return false
	}
	return true
}
