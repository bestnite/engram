package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「作业」页的 SPA JSON 端点（ROADMAP.md M6-6）。
//
// 读取复用 s.jobRunner.List，取消复用 s.jobRunner.Cancel 与 jobs.ErrNotRunning，与 SSR 页
// 完全同一份调用。kind/status/stage 是稳定英文标识，由前端映射文案。

// adminJob 是一个作业的元信息；时间为 UTC 文本，空串表示尚未发生。
type adminJob struct {
	ID         uint64  `json:"id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	Stage      *string `json:"stage"`
	CreatedAt  string  `json:"created_at"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	LogTail    string  `json:"log_tail"`
	Error      string  `json:"error"`
	CanCancel  bool    `json:"can_cancel"`
}

// adminJobsResponse 是作业列表的分页响应。
type adminJobsResponse struct {
	Jobs  []adminJob `json:"jobs"`
	Page  int        `json:"page"`
	Pages int        `json:"pages"`
	Total int64      `json:"total"`
}

// adminJobs 返回作业列表（分页），口径与 adminJobsPage 一致。
func (s *Server) adminJobs(c *gin.Context) {
	ctx := c.Request.Context()
	page := parsePage(c.Query("page"))
	rows := make([]adminJob, 0)
	var total int64
	pages := 1
	if s.jobRunner != nil {
		list, count, err := s.jobRunner.List(ctx, adminJobsPageSize, (page-1)*adminJobsPageSize)
		if err != nil {
			s.logger.Error("spa admin: list jobs failed", "error", err)
			adminError(c, http.StatusInternalServerError, "internal_error")
			return
		}
		total = count
		if pages = int((total + int64(adminJobsPageSize) - 1) / int64(adminJobsPageSize)); pages < 1 {
			pages = 1
		}
		for i := range list {
			job := list[i]
			row := adminJob{
				ID: job.ID, Kind: job.Kind, Status: job.Status, Stage: job.Stage,
				CreatedAt:  formatJobTime(&job.CreatedAt),
				StartedAt:  formatJobTime(job.StartedAt),
				FinishedAt: formatJobTime(job.FinishedAt),
				CanCancel:  job.Status == jobs.StatusQueued || job.Status == jobs.StatusRunning,
			}
			if job.LogTail != nil {
				row.LogTail = *job.LogTail
			}
			if job.Error != nil {
				row.Error = *job.Error
			}
			rows = append(rows, row)
		}
	}
	c.JSON(http.StatusOK, adminJobsResponse{Jobs: rows, Page: page, Pages: pages, Total: total})
}

// adminJobCancel 取消一个作业：运行中的会被杀进程组并标 failed，排队中的直接标 failed。
func (s *Server) adminJobCancel(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	id, err := parseUintParam(c.Param("id"))
	if err != nil {
		adminError(c, http.StatusNotFound, "invalid_job")
		return
	}
	if s.jobRunner == nil {
		adminError(c, http.StatusServiceUnavailable, "unavailable")
		return
	}
	ctx := c.Request.Context()
	if err := s.jobRunner.Cancel(ctx, id); err != nil {
		if errors.Is(err, jobs.ErrNotRunning) {
			adminError(c, http.StatusConflict, "not_running")
			return
		}
		s.logger.Error("spa admin: cancel job failed", "job_id", id, "error", err)
		adminError(c, http.StatusInternalServerError, "failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionJobCancel,
		TargetType: "job", TargetID: store.Ptr(id),
	})
	c.Status(http.StatusNoContent)
}
