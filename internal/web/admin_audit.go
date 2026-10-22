package web

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// Query parsing for admin panel audit search.
//
// Time convention: created_at in the DB is uniformly UTC; pages display it in the current admin's timezone,
// and date-range filtering converts the natural-day boundaries of the user's timezone into UTC instants before handing them to the store.
// After the SSR audit page was removed, search lives on the /api/v1/admin/audit JSON endpoint (admin_read.go),
// and filter parsing and page size are shared from here.

// adminAuditPageSize is the number of rows per page in the audit list.
const adminAuditPageSize = 50

// auditLocation returns the timezone the current admin uses for display and date conversion; see store.LoadLocation for the fallback rules.
func auditLocation(u *store.User) *time.Location {
	if u == nil {
		return time.UTC
	}
	return store.LoadLocation(u.Timezone)
}

// parseAuditDay parses YYYY-MM-DD in the given timezone into midnight of that day; ok=false when the format is invalid.
func parseAuditDay(raw string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// auditFilterFromQuery parses audit search query parameters into store.AuditFilter (shared by the JSON endpoint and the page).
//   - user accepts a public id or a username (neither is purely numeric); an unknown user hits the empty set instead of degrading to "no filter".
//   - target_id is a public id: the numeric primary key is looked up by target_type; an invalid date format returns a notice code (stable English),
//     which the caller maps to copy.
//
// The second return value is the user query string as-is, the third is the notice code (empty string means no notice).
func (s *Server) auditFilterFromQuery(c *gin.Context, userLoc *time.Location, page int) (store.AuditFilter, string, string) {
	ctx := c.Request.Context()
	userRaw := strings.TrimSpace(c.Query("user"))
	filter := store.AuditFilter{
		Action:     strings.TrimSpace(c.Query("action")),
		TargetType: strings.TrimSpace(c.Query("target_type")),
		Limit:      adminAuditPageSize,
		Offset:     (page - 1) * adminAuditPageSize,
	}
	notice := ""
	if userRaw != "" {
		switch {
		case s.users != nil && !isNumeric(userRaw):
			// The admin panel displays public ids, so look up by public id first, then fall back to lookup by username.
			if u, err := s.users.ByPublicID(ctx, userRaw); err == nil {
				filter.UserID = u.ID
			} else if u, err := s.users.ByUsername(ctx, userRaw); err == nil {
				filter.UserID = u.ID
			} else {
				// Unknown user: must not silently degrade to "no filter" (that would misreport "no such person" as the full result set).
				// Use an impossible id (the int64 upper bound) to hit the empty set, avoiding binding the uint64 high bit.
				filter.UserID = uint64(math.MaxInt64)
				notice = "user_not_found"
			}
		default:
			if id, err := strconv.ParseUint(userRaw, 10, 64); err == nil {
				filter.UserID = id
			} else {
				notice = "user_not_found"
			}
		}
	}
	if targetIDRaw := strings.TrimSpace(c.Query("target_id")); targetIDRaw != "" {
		// The target id is a public id externally: the numeric primary key is looked up by target_type, while also accepting legacy all-numeric input.
		if id, ok := s.auditTargetNumericID(ctx, filter.TargetType, targetIDRaw); ok {
			filter.TargetID = id
		} else if id, err := strconv.ParseUint(targetIDRaw, 10, 64); err == nil {
			filter.TargetID = id
		} else {
			// When it cannot be parsed, must not silently degrade to "no filter" (that would misreport "not found" as the full result set):
			// use an impossible id to hit the empty set, the same convention as the user filter.
			filter.TargetID = uint64(math.MaxInt64)
			notice = "invalid_target"
		}
	}
	if fromRaw := strings.TrimSpace(c.Query("from")); fromRaw != "" {
		if t, ok := parseAuditDay(fromRaw, userLoc); ok {
			filter.From = t
		} else {
			notice = "invalid_date"
		}
	}
	if toRaw := strings.TrimSpace(c.Query("to")); toRaw != "" {
		if t, ok := parseAuditDay(toRaw, userLoc); ok {
			// to is an inclusive-of-that-day upper bound, converted to midnight of the next day (exclusive), so the whole day falls inside the range.
			filter.To = t.AddDate(0, 0, 1)
		} else {
			notice = "invalid_date"
		}
	}
	return filter, userRaw, notice
}

// isNumeric reports whether a string is purely numeric (distinguishing "by id" from "by username" input).
func isNumeric(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// usersForAudit batch-fetches the users involved on the current page, for the audit rows' actor public id and username;
// users that cannot be found are left out, and the caller renders that row's user_id as null, never falling back to the numeric primary key.
func (s *Server) usersForAudit(ctx context.Context, rows []store.AuditLog) map[uint64]*store.User {
	users := make(map[uint64]*store.User)
	if s.users == nil {
		return users
	}
	for i := range rows {
		id := rows[i].UserID
		if id == nil {
			continue
		}
		if _, seen := users[*id]; seen {
			continue
		}
		if u, err := s.users.ByID(ctx, *id); err == nil {
			users[*id] = u
		}
	}
	return users
}

// auditTargetPublicID converts an audit row's numeric target id into a public id; when it cannot be resolved (unknown type or the target row was deleted)
// it returns nil, and the caller renders target.id as null accordingly, never falling back to the numeric primary key.
func (s *Server) auditTargetPublicID(ctx context.Context, targetType string, id *uint64) *string {
	if id == nil {
		return nil
	}
	var publicID string
	switch targetType {
	case "user":
		publicID = s.userPublicID(ctx, *id)
	case "deck":
		publicID = s.deckPublicID(ctx, *id)
	case "preset":
		publicID = s.presetPublicID(ctx, *id)
	case "job":
		if s.jobStore != nil {
			if job, err := s.jobStore.ByID(ctx, *id); err == nil {
				publicID = job.PublicID
			}
		}
	case "invite":
		// invites has no lookup-by-primary-key entry point (only by token or public id), so take public_id directly.
		_ = s.db.WithContext(ctx).Table("invites").Select("public_id").Where("id = ?", *id).Scan(&publicID).Error
	case "api_key":
		if k, err := store.NewAPIKeyStore(s.db).ByID(ctx, *id); err == nil {
			publicID = k.PublicID
		}
	case "identity":
		if s.identities != nil {
			if ident, err := s.identities.ByID(ctx, *id); err == nil {
				publicID = ident.PublicID
			}
		}
	case "card":
		if s.cards != nil {
			if c, err := s.cards.ByID(ctx, *id); err == nil {
				publicID = c.PublicID
			}
		}
	}
	if publicID == "" {
		return nil
	}
	return &publicID
}

// auditTargetNumericID is the reverse mapping of auditTargetPublicID: it converts a public id back to the numeric primary key by target type,
// for use by the target_id filter in audit search (the list shows public ids, so filtering must accept them too).
// Returns false when the type is unknown, the target row was deleted, or the corresponding store is missing; the caller then shows a notice instead of silently passing.
func (s *Server) auditTargetNumericID(ctx context.Context, targetType, publicID string) (uint64, bool) {
	switch targetType {
	case "user":
		if s.users != nil {
			if u, err := s.users.ByPublicID(ctx, publicID); err == nil {
				return u.ID, true
			}
		}
	case "deck":
		if s.decks != nil {
			if d, err := s.decks.ByPublicID(ctx, publicID); err == nil {
				return d.ID, true
			}
		}
	case "preset":
		if s.presets != nil {
			if p, err := s.presets.ByPublicID(ctx, publicID); err == nil {
				return p.ID, true
			}
		}
	case "job":
		if s.jobStore != nil {
			if j, err := s.jobStore.ByPublicID(ctx, publicID); err == nil {
				return j.ID, true
			}
		}
	case "invite":
		if s.invites != nil {
			if inv, err := s.invites.ByPublicID(ctx, publicID); err == nil {
				return inv.ID, true
			}
		}
	case "api_key":
		if k, err := store.NewAPIKeyStore(s.db).ByPublicID(ctx, publicID); err == nil {
			return k.ID, true
		}
	case "identity":
		if s.identities != nil {
			if ident, err := s.identities.ByPublicID(ctx, publicID); err == nil {
				return ident.ID, true
			}
		}
	case "card":
		if s.cards != nil {
			if c, err := s.cards.ByPublicID(ctx, publicID); err == nil {
				return c.ID, true
			}
		}
	}
	return 0, false
}
