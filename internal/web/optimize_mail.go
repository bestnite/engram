package web

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the sender for the type C "parameter optimization finished" (optimize_done) mail.
//
// It is called by the job success hook and has **no request context**: an absolute address can only be built from BASE_URL, and without it the unsubscribe
// entry is skipped (better one entry fewer than a relative link that does not open). The type belongs to the "default off" optional class, so only
// users who turned it on receive it.

// absoluteURLCtx builds an absolute address from BASE_URL; returns an empty string when unconfigured (the job path has no request to derive from).
func (s *Server) absoluteURLCtx(path string) string {
	base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/")
	if base == "" {
		return ""
	}
	return base + path
}

// optionalUnsubscribeLinkCtx is the same as optionalUnsubscribeLink but without a request context.
func (s *Server) optionalUnsubscribeLinkCtx(ctx context.Context, userID uint64, typ mail.Type) string {
	if s.tokens == nil || !mail.CanUnsubscribe(typ) {
		return ""
	}
	token, err := s.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(typ), auth.UnsubscribeTTL)
	if err != nil {
		s.logger.Error("unsubscribe: issue token failed", "user_id", userID, "type", string(typ), "error", err)
		return ""
	}
	link := s.absoluteURLCtx("/unsubscribe?token=" + url.QueryEscape(token))
	if link == "" {
		s.logger.Info("optimize mail: BASE_URL is not configured; unsubscribe link omitted")
	}
	return link
}

// NotifyOptimizeDone notifies the preset owner after a successful parameter optimization. The signature matches jobs.SuccessFunc.
//
// The job row records only TargetID (the preset id), so the owner must be looked up through the preset — the jobs table has no owner column, and should not gain one for this.
func (s *Server) NotifyOptimizeDone(ctx context.Context, job store.Job) {
	if job.Kind != jobs.KindOptimize || job.TargetID == nil || *job.TargetID == 0 {
		return
	}
	preset, err := s.presets.ByID(ctx, *job.TargetID)
	if err != nil || preset == nil {
		s.logger.Error("optimize mail: load preset failed", "preset_id", *job.TargetID, "error", err)
		return
	}
	owner, err := s.users.ByID(ctx, preset.OwnerUserID)
	if err != nil || owner == nil {
		s.logger.Error("optimize mail: load preset owner failed", "preset_id", preset.ID, "error", err)
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		s.logger.Info("optimize mail: smtp is not configured; notification skipped", "preset_id", preset.ID)
		return
	}
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, owner.ID)
	if err != nil {
		s.logger.Error("optimize mail: read recipient preferences failed", "user_id", owner.ID, "error", err)
		return
	}
	// Type C is off by default: only users who turned it on receive this mail.
	if !mail.ResolveEnabled(choices, mail.TypeOptimizeDone) {
		return
	}

	loc := s.userLocalizer(owner)
	site := securitySiteName(loc)
	link := s.absoluteURLCtx("/presets")
	unsubURL := s.optionalUnsubscribeLinkCtx(ctx, owner.ID, mail.TypeOptimizeDone)
	reviewCount := optimizeReviewCount(job)

	// The in-database name of the default preset is an identity marker (store.DefaultPresetName), not human-facing copy:
	// the mail body is user-visible text, so it is swapped for the display name from the language pack in the recipient's language, while user-created presets are shown as they are.
	presetName := preset.Name
	if preset.Name == store.DefaultPresetName {
		presetName = loc.T("presets.default_name")
	}

	vars := mail.Vars{
		"site": site, "preset": presetName,
		"count": strconv.Itoa(reviewCount), "url": link,
	}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	footerNote, unsubLabel := loc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	fallbackSubject, fallbackBodyMD, _ := mail.DefaultTemplate(mail.TypeOptimizeDone, mail.VariantDefault, loc.Tf, vars)
	tpl := mail.Resolve(s.mailTemplateLookup(), mail.TypeOptimizeDone, loc.Locale(), s.siteDefaultLocale(ctx))
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mail.TypeOptimizeDone,
		Site:             site,
		Lang:             loc.Locale(),
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fallbackSubject,
		FallbackBodyMD:   fallbackBodyMD,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{
		To: owner.Email, Type: string(mail.TypeOptimizeDone),
		Subject: out.Subject, TextBody: out.Text, HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeOptimizeDone, unsubURL)
	if err := s.mail.Enqueue(ctx, msg); err != nil {
		s.logger.Error("optimize mail: enqueue failed", "preset_id", preset.ID, "error", err)
	}
}

// optimizeReviewCount takes "how many reviews it used" from the job result, returning 0 when it is unavailable.
//
// It only feeds one number in the body, and a missing one must not affect sending, so it returns no error (it reads the same
// result_json as presets_api.go, with field names per store.OptimizeResult).
func optimizeReviewCount(job store.Job) int {
	if job.ResultJSON == nil || strings.TrimSpace(*job.ResultJSON) == "" {
		return 0
	}
	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*job.ResultJSON), &result); err != nil {
		return 0
	}
	return int(result.ReviewsUsed)
}
