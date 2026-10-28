package web

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// registerPresetRoutes mounts the scheduling preset page entry.
//
// The SSR page layer has been deleted: GET /presets only sends the app shell, and preset list/create/edit/optimize/rollback all go through
// the /api/v1/presets* JSON endpoints (presets_api.go, using the same set of store/jobs methods as the SSR page).
// Skipped when the dependency is not wired up, so M0-stage tests can still construct a Server.
func (s *Server) registerPresetRoutes(router *gin.Engine) {
	if s.sessions == nil || s.presets == nil || s.jobRunner == nil {
		return
	}
	router.GET("/presets", s.presetRoute)
	// SPA preset API: same-origin JSON reads and writes go through /api/v1/presets*.
	s.registerPresetAPIRoutes(router)
}

// optimizeJobFor reads an optimization job by public id and checks it really belongs to that preset (both kind and target match).
// Any mismatch returns nil: the status endpoint may only see this preset's own jobs.
func (s *Server) optimizeJobFor(ctx context.Context, presetID uint64, raw string) *store.Job {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	job, err := s.jobStore.ByPublicID(ctx, raw)
	if err != nil {
		return nil
	}
	if job.Kind != jobs.KindOptimize || job.TargetID == nil || *job.TargetID != presetID {
		return nil
	}
	return job
}

// validStepSpec reports whether a learn/relearn step string can be parsed; an empty string is valid (steps off).
// The grammar matches internal/schedule's parseSteps (comma-separated, s/m/h/d suffixes, minutes by default),
// but it additionally requires every step to be positive: the scheduler accepts negatives like "-5m" as valid input, and acceptance requires refusing them here.
// This only decides "parsable" and does not copy the scheduling logic — the scheduler still uses its own parseSteps when reading from the database.
func validStepSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := len(part)
		for idx > 0 {
			ch := part[idx-1]
			if (ch >= '0' && ch <= '9') || ch == '.' {
				break
			}
			idx--
		}
		num, unit := part[:idx], strings.ToLower(part[idx:])
		value, err := strconv.ParseFloat(num, 64)
		if err != nil || value <= 0 {
			return false
		}
		switch unit {
		case "", "m", "min", "s", "sec", "h", "hr", "d":
		default:
			return false
		}
	}
	return true
}
