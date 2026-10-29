package web

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the sender for the type C "parameter optimization finished" (optimize_done) mail.
//
// It is called by the job success hook and has **no request context**: links come from BASE_URL alone, and without it the
// mail carries no unsubscribe entry (mail.Mailer skips a link that would not open). The type belongs to the "default off"
// optional class, so only users who turned it on receive it.

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
	// The in-database name of the default preset is an identity marker (store.DefaultPresetName), not human-facing copy:
	// the mail body is user-visible text, so it is swapped for the display name from the language pack in the recipient's language, while user-created presets are shown as they are.
	loc := s.userLocalizer(owner)
	presetName := preset.Name
	if preset.Name == store.DefaultPresetName {
		presetName = loc.T("presets.default_name")
	}
	// Type C is off by default: mail.Mailer sends only to owners who turned it on.
	s.sendMail(ctx, loc, mail.Mail{
		Type: mail.TypeOptimizeDone, To: owner.Email, UserID: owner.ID,
		Vars: mail.Vars{
			"preset": presetName, "count": strconv.Itoa(optimizeReviewCount(job)), "url": s.mailer.URL("", "/presets"),
		},
	})
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
