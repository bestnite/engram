package web

import (
	"context"
	"strings"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is: class D admin notification mail.
//
// Of the three trigger points, two actually exist in this repository:
//   - Job failure: fail in internal/jobs/jobs.go (the Runner calls back into this file's
//     NotifyJobFailed via the OnFailure hook, wired up in cmd/engram/main.go);
//   - Media quota: checkMediaQuota in internal/web/media.go (when a user upload is blocked by the quota).
//
// The three hard constraints match class A/B mail and are implemented one by one:
//   - Never send mail synchronously on the request path: always enqueue via mail.AdminNotifier.Send → Outbox.Enqueue;
//   - A send failure must not fail the triggering operation: no trigger function returns an error, failures are only logged in English;
//   - Must not be silent when SMTP is unconfigured: the trigger point logs a Warn, and the SMTP config page renders the mail.not_configured notice.
//
// Recipient address: class D goes to the sign-in mailbox of admin accounts (store.UserStore.ListAdmins).
// Preference: class D is on by default and can be turned off, decided by the **receiving admin's own** preference (mail.ResolveEnabled).

// adminRecipients returns all admins who can still sign in; on query failure it logs and returns an empty list.
// This only resolves addresses and never fails the triggering operation because of a query failure.
func (s *Server) adminRecipients(ctx context.Context) []store.User {
	if s.users == nil {
		return nil
	}
	admins, err := s.users.ListAdmins(ctx)
	if err != nil {
		s.logger.Error("admin mail: list admins failed", "error", err)
		return nil
	}
	return admins
}

// notifyAdmins sends one class D notification to each admin: localized into each one's language, decided by each one's preference, enqueued one by one.
// It returns no error — no mail problem may fail the triggering operation; the return value is only the number of messages successfully enqueued (for tests and
// callers to observe, not for callers to handle).
//
// build takes the receiving admin's localizer and returns the mail subject and body in that admin's language.
func (s *Server) notifyAdmins(ctx context.Context, t mail.Type, build func(loc *i18n.Localizer) mail.Message) int {
	if s.mail == nil || !s.mail.Configured() {
		// SMTP unconfigured: must not be silent — log a Warn and let the triggering operation proceed as usual. The relevant pages render mail.not_configured.
		s.logger.Warn("admin mail: smtp is not configured; notification not sent", "type", string(t))
		return 0
	}
	admins := s.adminRecipients(ctx)
	notifier := mail.NewAdminNotifier(s.mail, store.NewEmailPrefStore(s.db), s.logger)
	sent := 0
	for i := range admins {
		admin := &admins[i]
		to := strings.TrimSpace(admin.Email)
		if to == "" {
			continue
		}
		msg := build(s.userLocalizer(admin))
		if notifier.Send(ctx, admin.ID, to, t, msg.Subject, msg.TextBody, msg.HTMLBody) {
			sent++
		}
	}
	return sent
}

// NotifyJobFailed notifies admins after a job failure is persisted (class D, job_failed).
// The signature matches jobs.FailureFunc; cmd/engram hooks it up to Runner.OnFailure.
// It runs in the job worker's context, so it must return quickly and never panic (the jobs side already wraps recover as a backstop).
func (s *Server) NotifyJobFailed(ctx context.Context, job store.Job, reason string) {
	s.notifyAdmins(ctx, mail.TypeJobFailed, func(loc *i18n.Localizer) mail.Message {
		return s.adminJobFailedMessage(ctx, loc, securitySiteName(loc), job, reason)
	})
}

// NotifyMediaAlert notifies admins when a user's media quota is triggered (class D, media_disk_alert).
// Called by checkMediaQuota after writing the 413; deliberately returns no error so it can never affect the upload's error response.
func (s *Server) NotifyMediaAlert(ctx context.Context, u *store.User, used, quota int64) {
	if u == nil {
		return
	}
	s.notifyAdmins(ctx, mail.TypeMediaDiskAlert, func(loc *i18n.Localizer) mail.Message {
		return s.adminMediaAlertMessage(ctx, loc, securitySiteName(loc), *u, used, quota)
	})
}

// ── Copy assembly for the two kinds of mail (the default body is plain text only; an HTML part is produced only when the admin configured a template) ──

// adminJobFailedMessage assembles the "job failed" notification; reason is the failure reason (English, from jobs.error).
func (s *Server) adminJobFailedMessage(ctx context.Context, loc *i18n.Localizer, site string, job store.Job, reason string) mail.Message {
	vars := mail.Vars{
		"site": site, "job_id": job.PublicID, "kind": job.Kind, "reason": reason,
	}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeJobFailed, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeJobFailed, vars, fbSubject, fbText, "")
	return mail.Message{Subject: subject, TextBody: text, HTMLBody: htmlBody}
}

// adminMediaAlertMessage assembles the "media quota" alert; used/quota are byte counts (human-readable when displayed).
func (s *Server) adminMediaAlertMessage(ctx context.Context, loc *i18n.Localizer, site string, u store.User, used, quota int64) mail.Message {
	usedText, limitText := humanBytes(used), humanBytes(quota)
	vars := mail.Vars{"site": site, "username": u.Username, "used": usedText, "limit": limitText}
	fbSubject, fbText, _ := mail.DefaultTemplate(mail.TypeMediaDiskAlert, mail.VariantDefault, loc.Tf, vars)
	subject, text, htmlBody := s.renderMail(ctx, loc, mail.TypeMediaDiskAlert, vars, fbSubject, fbText, "")
	return mail.Message{Subject: subject, TextBody: text, HTMLBody: htmlBody}
}
