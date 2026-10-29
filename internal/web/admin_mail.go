package web

import (
	"context"
	"strings"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is: class D admin notification mail.
//
// Two of the three trigger points named by the class-D plan exist in this repository:
//   - Job failure: fail in internal/jobs/jobs.go (the Runner calls back into this file's
//     NotifyJobFailed via the OnFailure hook, wired up in cmd/engram/main.go);
//   - Media quota: checkMediaQuota in internal/web/media.go (when a user upload is blocked by the quota).
//
// The three hard constraints match class A/B mail and are implemented one by one:
//   - Never send mail synchronously on the request path: always enqueue via mail.Mailer.Send → Outbox.Enqueue;
//   - A send failure must not fail the triggering operation: no trigger function returns an error, failures are only logged in English;
//   - Must not be silent when SMTP is unconfigured: the trigger point logs a Warn, and the SMTP config page renders a "mail is not configured" notice.
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

// notifyAdmins sends one class D notification to each admin: localized into each one's language, decided by each one's
// preference and carrying each one's unsubscribe link (mail.Mailer decides all three). It returns nothing — no mail
// problem may fail the triggering operation.
func (s *Server) notifyAdmins(ctx context.Context, t mail.Type, vars mail.Vars) {
	if !s.mailer.Configured() {
		// SMTP unconfigured: must not be silent — log a Warn and let the triggering operation proceed as usual. The relevant pages render a "mail is not configured" notice.
		s.logger.Warn("admin mail: smtp is not configured; notification not sent", "type", string(t))
		return
	}
	for _, admin := range s.adminRecipients(ctx) {
		to := strings.TrimSpace(admin.Email)
		if to == "" {
			continue
		}
		s.sendMail(ctx, s.userLocalizer(&admin), mail.Mail{Type: t, To: to, UserID: admin.ID, Vars: vars})
	}
}

// NotifyJobFailed notifies admins after a job failure is persisted (class D, job_failed); reason is the failure reason
// (English, from jobs.error). The signature matches jobs.FailureFunc; cmd/engram hooks it up to Runner.OnFailure.
// It runs in the job worker's context, so it must return quickly and never panic (the jobs side already wraps recover as a backstop).
func (s *Server) NotifyJobFailed(ctx context.Context, job store.Job, reason string) {
	s.notifyAdmins(ctx, mail.TypeJobFailed, mail.Vars{"job_id": job.PublicID, "kind": job.Kind, "reason": reason})
}

// NotifyMediaAlert notifies admins when a user's media quota is triggered (class D, media_disk_alert); used/quota are
// byte counts, shown human-readable. Called by checkMediaQuota after writing the 413; deliberately returns no error so
// it can never affect the upload's error response.
func (s *Server) NotifyMediaAlert(ctx context.Context, u *store.User, used, quota int64) {
	if u == nil {
		return
	}
	s.notifyAdmins(ctx, mail.TypeMediaDiskAlert, mail.Vars{"username": u.Username, "used": humanBytes(used), "limit": humanBytes(quota)})
}
