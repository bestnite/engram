package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板只读页（概览 / 健康 / 审计）的 SPA JSON 端点。
//
// 口径与各自 SSR 页逐项一致：概览调 store.InstanceSummary，健康页现算
// 四项读数，审计检索走 store.AuditStore.Search。响应只带原始值与稳定英文标识，本地化文案
// 一律由前端语言包按标识映射，因此这里不返回任何本地化文本。

// adminSummaryResponse 是概览页（/admin）的计数卡数据。
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

// adminSummary 返回实例级计数。任何一项查询失败整体 500——空实例与查询失败不该同形。
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

// adminHealthResponse 是健康页的读数。
// schema_version / due 为 null 表示读不出来（与 SSR 的「未知」占位同义）；
// media_bytes 是原始字节数，前端负责人类可读格式化。
type adminHealthResponse struct {
	Database       string `json:"database"` // ok | error
	SchemaVersion  *int   `json:"schema_version"`
	MediaBytes     int64  `json:"media_bytes"`
	MediaTruncated bool   `json:"media_truncated"`
	Due            *int64 `json:"due"`
}

// adminHealth 返回健康页的四项读数，口径与 adminHealthPage 完全相同。
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

// adminAuditActor 是审计行里的操作者；nil 表示系统动作（无 UserID）。
type adminAuditActor struct {
	UserID   *uint64 `json:"user_id"`
	Username string  `json:"username"`
}

// adminAuditTarget 是审计行里的目标对象；nil 表示无目标。
type adminAuditTarget struct {
	Type string  `json:"type"`
	ID   *uint64 `json:"id"`
}

// adminAuditRow 是一行审计记录。时间已按当前管理员时区格式化。
type adminAuditRow struct {
	Time   string            `json:"time"`
	Actor  *adminAuditActor  `json:"actor"`
	Action string            `json:"action"`
	Target *adminAuditTarget `json:"target"`
	// Detail 为原始 JSON 串；空串表示无详情。
	Detail string `json:"detail"`
}

// adminAuditResponse 是审计检索的响应；notice 是稳定英文码（空串表示无提示）。
type adminAuditResponse struct {
	Rows    []adminAuditRow `json:"rows"`
	Actions []string        `json:"actions"`
	Page    int             `json:"page"`
	Pages   int             `json:"pages"`
	Total   int64           `json:"total"`
	Notice  string          `json:"notice"`
}

// adminAudit 返回审计检索结果，过滤与分页语义与 adminAuditPage 完全一致。
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

	names := s.usernamesFor(ctx, list)
	rows := make([]adminAuditRow, 0, len(list))
	for i := range list {
		row := list[i]
		out := adminAuditRow{
			Time:   row.CreatedAt.In(userLoc).Format("2006-01-02 15:04:05"),
			Action: row.Action,
		}
		if row.UserID != nil {
			actor := &adminAuditActor{UserID: row.UserID}
			if name, ok := names[*row.UserID]; ok {
				actor.Username = name
			}
			out.Actor = actor
		}
		if row.TargetType != nil && *row.TargetType != "" {
			out.Target = &adminAuditTarget{Type: *row.TargetType, ID: row.TargetID}
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
