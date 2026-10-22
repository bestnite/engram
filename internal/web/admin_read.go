package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's read-only pages (overview / health / audit).
//
// Conventions match each SSR page item by item: the overview calls store.InstanceSummary, the health page computes
// four readings on the fly, and audit search goes through store.AuditStore.Search. Responses carry only raw values and stable English identifiers; localized copy
// is always mapped from those identifiers by the frontend language packs, so no localized text is returned here.

// adminSummaryResponse is the count-card data for the overview page (/admin).
type adminSummaryResponse struct {
	UsersTotal  int64 `json:"users_total"`
	UsersActive int64 `json:"users_active"`
	Decks       int64 `json:"decks"`
	Notes       int64 `json:"notes"`
	Cards       int64 `json:"cards"`
	Due         int64 `json:"due"`
	JobsRunning int64 `json:"jobs_running"`
	JobsFailed  int64 `json:"jobs_failed"`
}

// adminSummary returns instance-level counts. A failure of any single query turns the whole thing into 500 — an empty instance and a query failure should not look alike.
func (s *Server) adminSummary(c *gin.Context) {
	stats, err := store.InstanceSummary(c.Request.Context(), s.db, time.Now().UTC())
	if err != nil {
		s.logger.Error("spa admin: instance summary failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	c.JSON(http.StatusOK, adminSummaryResponse{
		UsersTotal: stats.Users, UsersActive: stats.ActiveUsers,
		Decks: stats.Decks, Notes: stats.Notes, Cards: stats.Cards,
		Due: stats.DueNow, JobsRunning: stats.JobsRunning, JobsFailed: stats.JobsFailed,
	})
}

// adminHealthResponse is the health page's readings.
// schema_version / due being null means it cannot be read (synonymous with SSR's "unknown" placeholder);
// media_bytes is the raw byte count, and human-readable formatting is the frontend's job.
type adminHealthResponse struct {
	Database       string `json:"database"` // ok | error
	SchemaVersion  *int   `json:"schema_version"`
	MediaBytes     int64  `json:"media_bytes"`
	MediaTruncated bool   `json:"media_truncated"`
	Due            *int64 `json:"due"`
}

// adminHealth returns the health page's four readings, with exactly the same convention as adminHealthPage.
func (s *Server) adminHealth(c *gin.Context) {
	ctx := c.Request.Context()
	resp := adminHealthResponse{Database: "ok"}

	if sqlDB, err := s.db.DB(); err != nil {
		resp.Database = "error"
		s.logger.Error("spa admin health: cannot get sql.DB", "error", err)
	} else if err := sqlDB.PingContext(ctx); err != nil {
		resp.Database = "error"
		s.logger.Error("spa admin health: database ping failed", "error", err)
	}

	if v, err := s.schemaVersion(ctx); err == nil {
		resp.SchemaVersion = &v
	} else {
		s.logger.Error("spa admin health: cannot read schema version", "error", err)
	}

	size, truncated := s.mediaUsage()
	resp.MediaBytes, resp.MediaTruncated = size, truncated

	if n, err := store.DueQueueSize(ctx, s.db, time.Now().UTC()); err == nil {
		resp.Due = &n
	} else {
		s.logger.Error("spa admin health: cannot count due queue", "error", err)
	}

	c.JSON(http.StatusOK, resp)
}

// adminAuditActor is the actor in an audit row; nil means a system action (no UserID).
// user_id is a public id; null when the user row no longer exists (never falls back to the numeric primary key).
type adminAuditActor struct {
	UserID   *string `json:"user_id"`
	Username string  `json:"username"`
}

// adminAuditTarget is the target object in an audit row; nil means no target. id is a public id.
type adminAuditTarget struct {
	Type string  `json:"type"`
	ID   *string `json:"id"`
}

// adminAuditRow is one audit record. Times are already formatted in the current admin's timezone.
type adminAuditRow struct {
	Time   string            `json:"time"`
	Actor  *adminAuditActor  `json:"actor"`
	Action string            `json:"action"`
	Target *adminAuditTarget `json:"target"`
	// Detail is the raw JSON string; an empty string means no detail.
	Detail string `json:"detail"`
}

// adminAuditResponse is the audit search response; notice is a stable English code (empty string means no notice).
type adminAuditResponse struct {
	Rows    []adminAuditRow `json:"rows"`
	Actions []string        `json:"actions"`
	Page    int             `json:"page"`
	Pages   int             `json:"pages"`
	Total   int64           `json:"total"`
	Notice  string          `json:"notice"`
}

// adminAudit returns audit search results, with filter and pagination semantics exactly matching adminAuditPage.
func (s *Server) adminAudit(c *gin.Context) {
	ctx := c.Request.Context()
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)
	page := parsePage(c.Query("page"))

	filter, _, notice := s.auditFilterFromQuery(c, userLoc, page)
	auditStore := store.NewAuditStore(s.db)
	list, total, err := auditStore.Search(ctx, filter)
	if err != nil {
		s.logger.Error("spa admin: search audit log failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	pages := int((total + int64(adminAuditPageSize) - 1) / int64(adminAuditPageSize))
	if pages < 1 {
		pages = 1
	}

	actors := s.usersForAudit(ctx, list)
	rows := make([]adminAuditRow, 0, len(list))
	for i := range list {
		row := list[i]
		out := adminAuditRow{
			Time:   row.CreatedAt.In(userLoc).Format("2006-01-02 15:04:05"),
			Action: row.Action,
		}
		if row.UserID != nil {
			actor := &adminAuditActor{}
			if u, ok := actors[*row.UserID]; ok {
				actor.UserID = &u.PublicID
				actor.Username = u.Username
			}
			out.Actor = actor
		}
		if row.TargetType != nil && *row.TargetType != "" {
			out.Target = &adminAuditTarget{Type: *row.TargetType, ID: s.auditTargetPublicID(ctx, *row.TargetType, row.TargetID)}
		}
		if row.DetailJSON != nil {
			out.Detail = *row.DetailJSON
		}
		rows = append(rows, out)
	}

	actions := make([]string, 0, 8)
	if distinct, err := auditStore.DistinctActions(ctx); err == nil {
		actions = distinct
	}

	c.JSON(http.StatusOK, adminAuditResponse{
		Rows: rows, Actions: actions,
		Page: page, Pages: pages, Total: total,
		Notice: notice,
	})
}
