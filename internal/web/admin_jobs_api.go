package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the SPA JSON endpoint for the admin panel's "jobs" page.
//
// Reads reuse s.jobRunner.List, cancellation reuses s.jobRunner.Cancel and jobs.ErrNotRunning, exactly the same calls as the SSR
// page. kind/status/stage are stable English identifiers whose copy is mapped by the frontend.

// adminJob is a job's metadata; times are UTC text, and an empty string means it has not happened yet.
type adminJob struct {
	ID         string  `json:"id"`
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

// adminJobsResponse is the paged response for the job list.
type adminJobsResponse struct {
	Jobs  []adminJob `json:"jobs"`
	Page  int        `json:"page"`
	Pages int        `json:"pages"`
	Total int64      `json:"total"`
}

// adminJobs returns the job list (paged), with the same convention as adminJobsPage.
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
				ID: job.PublicID, Kind: job.Kind, Status: job.Status, Stage: job.Stage,
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

// adminJobCancel cancels a job: a running one has its process group killed and is marked failed, a queued one is marked failed directly.
func (s *Server) adminJobCancel(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	raw := strings.TrimSpace(c.Param("id"))
	if raw == "" || s.jobStore == nil {
		adminError(c, http.StatusNotFound, "invalid_job")
		return
	}
	if s.jobRunner == nil {
		adminError(c, http.StatusServiceUnavailable, "unavailable")
		return
	}
	ctx := c.Request.Context()
	job, err := s.jobStore.ByPublicID(ctx, raw)
	if err != nil {
		adminError(c, http.StatusNotFound, "invalid_job")
		return
	}
	if err := s.jobRunner.Cancel(ctx, job.ID); err != nil {
		if errors.Is(err, jobs.ErrNotRunning) {
			adminError(c, http.StatusConflict, "not_running")
			return
		}
		s.logger.Error("spa admin: cancel job failed", "job_id", job.ID, "error", err)
		adminError(c, http.StatusInternalServerError, "failed")
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionJobCancel,
		TargetType: "job", TargetID: store.Ptr(job.ID),
	})
	c.Status(http.StatusNoContent)
}
