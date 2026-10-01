package web

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web/views"
)

// 管理面板的健康页（DESIGN.md §8.4；AGENTS.md §5 M6-8）。
//
// 四个读数，全部现算或走缓存采样：
//   - 数据库连通：拿底层 sql.DB 做一次 Ping。
//   - schema 版本：与 /healthz 同源（s.schemaVersion）。
//   - 媒体目录占用：走 mediaUsage（有上限 + TTL 缓存，见 media_size.go 的取舍说明）。
//   - 当前到期队列量：store.DueQueueSize，口径写在该函数上。
//
// 纯读页，无写操作，因此不需要 CSRF；路由在 adminRoutes() 清单里，非 admin 一律 403。

// adminHealthPage 渲染健康页。
func (s *Server) adminHealthPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	rows := make([]views.AdminHealthRow, 0, 4)

	// 1) 数据库连通。
	dbValue := loc.T("admin.health.database.ok")
	if sqlDB, err := s.db.DB(); err != nil {
		dbValue = loc.T("admin.health.database.error")
		s.logger.Error("health page: cannot get sql.DB", "error", err)
	} else if err := sqlDB.PingContext(ctx); err != nil {
		dbValue = loc.T("admin.health.database.error")
		s.logger.Error("health page: database ping failed", "error", err)
	}
	rows = append(rows, views.AdminHealthRow{
		Key: "database", Label: loc.T("admin.health.database.label"),
		Value: dbValue, Hint: loc.T("admin.health.database.hint"),
	})

	// 2) schema 版本。
	schemaValue := loc.T("admin.health.schema.unknown")
	if v, err := s.schemaVersion(ctx); err == nil {
		schemaValue = strconv.Itoa(v)
	} else {
		s.logger.Error("health page: cannot read schema version", "error", err)
	}
	rows = append(rows, views.AdminHealthRow{
		Key: "schema", Label: loc.T("admin.health.schema.label"),
		Value: schemaValue, Hint: loc.T("admin.health.schema.hint"),
	})

	// 3) 媒体目录占用。
	size, truncated := s.mediaUsage()
	mediaValue := humanBytes(size)
	if truncated {
		mediaValue += " " + loc.T("admin.health.media.truncated")
	}
	rows = append(rows, views.AdminHealthRow{
		Key: "media", Label: loc.T("admin.health.media.label"),
		Value: mediaValue, Hint: loc.T("admin.health.media.hint"),
	})

	// 4) 当前到期队列量。
	dueValue := loc.T("admin.health.due.unknown")
	if n, err := store.DueQueueSize(ctx, s.db, time.Now().UTC()); err == nil {
		dueValue = strconv.FormatInt(n, 10)
	} else {
		s.logger.Error("health page: cannot count due queue", "error", err)
	}
	rows = append(rows, views.AdminHealthRow{
		Key: "due", Label: loc.T("admin.health.due.label"),
		Value: dueValue, Hint: loc.T("admin.health.due.hint"),
	})

	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.health.title", "/admin/health"),
		Heading:    loc.T("admin.health.heading"),
		Intro:      loc.T("admin.health.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/health"),

		HealthPage: true,
		HealthRows: rows,
	}))
}
