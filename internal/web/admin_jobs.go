package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 管理面板的作业页（DESIGN.md §8.4；ROADMAP.md M6-6）。
//
// 安全底线：
//   - 路由在 adminRoutes() 清单里，非 admin 一律 403（守卫先于 handler）。
//   - 取消是写操作，过 CSRF 中间件并写审计（job.cancel）。
//   - 列表限量分页，绝不一次性渲染整表；日志尾巴经 templ 文本节点自动转义。

// adminJobsPageSize 是作业列表每页行数。
const adminJobsPageSize = 20

// jobsNotice 把重定向回带的 notice 码翻成文案；未知码不显示。
func (s *Server) jobsNotice(loc *i18n.Localizer, code string) string {
	switch code {
	case "cancelled":
		return loc.T("admin.jobs.notice.cancelled")
	case "not_running":
		return loc.T("admin.jobs.notice.not_running")
	case "invalid_job":
		return loc.T("admin.jobs.notice.invalid_job")
	case "unavailable":
		return loc.T("admin.jobs.notice.unavailable")
	case "failed":
		return loc.T("admin.jobs.notice.failed")
	default:
		return ""
	}
}

// jobsStatusLabel 把作业状态翻成显示文案。
func jobsStatusLabel(loc *i18n.Localizer, status string) string {
	switch status {
	case jobs.StatusQueued:
		return loc.T("admin.jobs.status.queued")
	case jobs.StatusRunning:
		return loc.T("admin.jobs.status.running")
	case jobs.StatusSucceeded:
		return loc.T("admin.jobs.status.succeeded")
	case jobs.StatusFailed:
		return loc.T("admin.jobs.status.failed")
	default:
		return status
	}
}

// jobsStageLabel 把阶段名翻成显示文案；未进入训练阶段时显示占位。
func jobsStageLabel(loc *i18n.Localizer, stage *string) string {
	if stage == nil || *stage == "" {
		return loc.T("admin.jobs.stage.unknown")
	}
	switch *stage {
	case jobs.StageReadLogs:
		return loc.T("admin.jobs.stage.read_logs")
	case jobs.StageTraining:
		return loc.T("admin.jobs.stage.training")
	case jobs.StageWriting:
		return loc.T("admin.jobs.stage.writing")
	default:
		return *stage
	}
}

// jobsKindLabel 把作业类型翻成显示文案。
func jobsKindLabel(loc *i18n.Localizer, kind string) string {
	if kind == jobs.KindOptimize {
		return loc.T("admin.jobs.kind.optimize")
	}
	return kind
}

// formatJobTime 格式化可空时间戳；nil 返回空串（模板据此不渲染该行时间）。
func formatJobTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

// jobsPageHref 拼作业列表分页链接。
func jobsPageHref(page int) string {
	return "/admin/jobs?page=" + strconv.Itoa(page)
}

// adminJobsPage 渲染作业列表：状态、阶段、日志尾巴、时间与取消入口。
func (s *Server) adminJobsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	page := parsePage(c.Query("page"))
	rows := make([]views.AdminJobRow, 0)
	var total int64
	pages := 1
	if s.jobRunner != nil {
		list, count, err := s.jobRunner.List(ctx, adminJobsPageSize, (page-1)*adminJobsPageSize)
		if err != nil {
			s.logger.Error("admin: list jobs failed", "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		total = count
		if pages = int((total + int64(adminJobsPageSize) - 1) / int64(adminJobsPageSize)); pages < 1 {
			pages = 1
		}
		for i := range list {
			job := list[i]
			row := views.AdminJobRow{
				ID:          job.ID,
				KindLabel:   jobsKindLabel(loc, job.Kind),
				Status:      job.Status,
				StatusLabel: jobsStatusLabel(loc, job.Status),
				StageLabel:  jobsStageLabel(loc, job.Stage),
				CreatedAt:   formatJobTime(&job.CreatedAt),
				StartedAt:   formatJobTime(job.StartedAt),
				FinishedAt:  formatJobTime(job.FinishedAt),
				CanCancel:   job.Status == jobs.StatusQueued || job.Status == jobs.StatusRunning,
				CancelHref:  "/admin/jobs/" + strconv.FormatUint(job.ID, 10) + "/cancel",
			}
			if job.LogTail != nil {
				row.LogTail = *job.LogTail
			}
			if job.Error != nil {
				row.ErrorText = *job.Error
			}
			rows = append(rows, row)
		}
	}
	prevHref, nextHref := "", ""
	if page > 1 {
		prevHref = jobsPageHref(page - 1)
	}
	if page < pages {
		nextHref = jobsPageHref(page + 1)
	}
	csrf := ""
	if sess, ok := auth.CurrentSession(c); ok {
		csrf = sess.CSRFToken
	}
	renderHTMLStatus(c, http.StatusOK, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.jobs.title", "/admin/jobs"),
		Heading:    loc.T("admin.jobs.heading"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/jobs"),
		Notice:     s.jobsNotice(loc, c.Query("notice")),
		CSRF:       csrf,

		JobsPage: true,
		Jobs:     rows,
		Page:     page,
		Pages:    pages,
		Total:    total,

		ColJobID:      loc.T("admin.jobs.col.id"),
		ColJobKind:    loc.T("admin.jobs.col.kind"),
		ColJobStatus:  loc.T("admin.jobs.col.status"),
		ColJobStage:   loc.T("admin.jobs.col.stage"),
		ColJobCreated: loc.T("admin.jobs.col.created"),
		ColJobLog:     loc.T("admin.jobs.col.log"),
		ColJobActions: loc.T("admin.jobs.col.actions"),

		JobCancelLabel:   loc.T("admin.jobs.cancel"),
		JobCancelConfirm: loc.T("admin.jobs.confirm"),
		JobLogEmptyLabel: loc.T("admin.jobs.log_empty"),

		PrevHref: prevHref, NextHref: nextHref,
		PrevLabel: loc.T("admin.jobs.prev"), NextLabel: loc.T("admin.jobs.next"),
		EmptyLabel: loc.T("admin.jobs.empty"),
	}))
}

// adminJobCancel 取消一个作业：运行中的会被杀进程组并标 failed，排队中的直接标 failed。
func (s *Server) adminJobCancel(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/admin/jobs?notice=invalid_job")
		return
	}
	if s.jobRunner == nil {
		c.Redirect(http.StatusSeeOther, "/admin/jobs?notice=unavailable")
		return
	}
	ctx := c.Request.Context()
	if err := s.jobRunner.Cancel(ctx, id); err != nil {
		if errors.Is(err, jobs.ErrNotRunning) {
			c.Redirect(http.StatusSeeOther, "/admin/jobs?notice=not_running")
			return
		}
		s.logger.Error("admin: cancel job failed", "job_id", id, "error", err)
		c.Redirect(http.StatusSeeOther, "/admin/jobs?notice=failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(actor.ID),
		Action:     store.ActionJobCancel,
		TargetType: "job",
		TargetID:   store.Ptr(id),
	})
	c.Redirect(http.StatusSeeOther, "/admin/jobs?notice=cancelled")
}
