package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
)

// This file holds the SPA JSON endpoints of the admin panel's "jobs" page: the job history, cancelling an optimizer
// job, the periodic task overview and starting a periodic round by hand.
//
// The history reads s.jobStore.List, so periodic rows are listed even where no optimizer runner is wired; cancellation reuses
// s.jobRunner.Cancel and jobs.ErrNotRunning. The overview and the manual run go through the worker registry.
// kind/status/stage/trigger and the summary keys are stable English identifiers whose copy is mapped by the frontend.

// adminJob is a job's metadata; times are already formatted in the current admin's timezone, and an empty string
// means it has not happened yet.
type adminJob struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Trigger    string  `json:"trigger"`
	Status     string  `json:"status"`
	Stage      *string `json:"stage"`
	CreatedAt  string  `json:"created_at"`
	StartedAt  string  `json:"started_at"`
	FinishedAt string  `json:"finished_at"`
	// DurationMS is null until the job has both started and finished.
	DurationMS *int64 `json:"duration_ms"`
	// Summary is the action counts of a periodic round, null for an optimizer job: its result carries the fitted
	// weights, which belong to the preset owner and stay off the admin list.
	Summary   map[string]int64 `json:"summary"`
	LogTail   string           `json:"log_tail"`
	Error     string           `json:"error"`
	CanCancel bool             `json:"can_cancel"`
}

// adminJobsResponse is the paged response for the job list.
type adminJobsResponse struct {
	Jobs  []adminJob `json:"jobs"`
	Page  int        `json:"page"`
	Pages int        `json:"pages"`
	Total int64      `json:"total"`
}

// adminJobs returns the job list (paged), optionally narrowed to one kind with ?kind=; a kind that is neither the
// optimizer's nor a periodic task's is 400 invalid_kind.
func (s *Server) adminJobs(c *gin.Context) {
	ctx := c.Request.Context()
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)
	page := parsePage(c.Query("page"))
	kind := strings.TrimSpace(c.Query("kind"))
	if kind != "" && kind != jobs.KindOptimize && !worker.IsKind(kind) {
		adminError(c, http.StatusBadRequest, "invalid_kind")
		return
	}
	list, total, err := s.jobStore.List(ctx, kind, adminJobsPageSize, (page-1)*adminJobsPageSize)
	if err != nil {
		s.logger.Error("spa admin: list jobs failed", "error", err)
		adminError(c, http.StatusInternalServerError, "internal_error")
		return
	}
	pages := int((total + int64(adminJobsPageSize) - 1) / int64(adminJobsPageSize))
	if pages < 1 {
		pages = 1
	}
	rows := make([]adminJob, 0, len(list))
	for i := range list {
		rows = append(rows, adminJobRow(&list[i], userLoc))
	}
	c.JSON(http.StatusOK, adminJobsResponse{Jobs: rows, Page: page, Pages: pages, Total: total})
}

// adminJobRow converts one jobs row into the list item, with its times shown in loc.
func adminJobRow(job *store.Job, loc *time.Location) adminJob {
	row := adminJob{
		ID: job.PublicID, Kind: job.Kind, Status: job.Status, Stage: job.Stage,
		// A NULL trigger was written before the column existed, when only a user's optimization request wrote this table.
		Trigger:    jobs.TriggerUser,
		CreatedAt:  formatJobTime(&job.CreatedAt, loc),
		StartedAt:  formatJobTime(job.StartedAt, loc),
		FinishedAt: formatJobTime(job.FinishedAt, loc),
		CanCancel:  job.Status == jobs.StatusQueued || job.Status == jobs.StatusRunning,
	}
	if job.Trigger != nil {
		row.Trigger = *job.Trigger
	}
	if job.StartedAt != nil && job.FinishedAt != nil {
		ms := job.FinishedAt.Sub(*job.StartedAt).Milliseconds()
		row.DurationMS = &ms
	}
	if worker.IsKind(job.Kind) && job.ResultJSON != nil {
		var summary map[string]int64
		if err := json.Unmarshal([]byte(*job.ResultJSON), &summary); err == nil {
			row.Summary = summary
		}
	}
	if job.LogTail != nil {
		row.LogTail = *job.LogTail
	}
	if job.Error != nil {
		row.Error = *job.Error
	}
	return row
}

// adminTaskRound is the latest finished round of a periodic task.
type adminTaskRound struct {
	Trigger    string           `json:"trigger"`
	StartedAt  string           `json:"started_at"`
	FinishedAt string           `json:"finished_at"`
	Status     string           `json:"status"`
	Error      string           `json:"error"`
	Summary    map[string]int64 `json:"summary"`
}

// adminTask is one periodic task in the overview. The state is in-memory and covers this process only: a restart
// starts with the round each worker runs on start.
type adminTask struct {
	Kind            string          `json:"kind"`
	IntervalSeconds int64           `json:"interval_seconds"`
	Running         bool            `json:"running"`
	Pending         bool            `json:"pending"`
	NextRunAt       string          `json:"next_run_at"`
	Last            *adminTaskRound `json:"last"`
}

// adminTasksResponse is the task overview.
type adminTasksResponse struct {
	Tasks []adminTask `json:"tasks"`
}

// adminTasks returns the status of every registered periodic task, in the order of worker.Kinds.
func (s *Server) adminTasks(c *gin.Context) {
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)
	out := adminTasksResponse{Tasks: make([]adminTask, 0, len(worker.Kinds))}
	if s.tasks != nil {
		for _, st := range s.tasks.Statuses() {
			out.Tasks = append(out.Tasks, adminTaskPayload(st, userLoc))
		}
	}
	c.JSON(http.StatusOK, out)
}

// adminTaskPayload converts a lifecycle status into the overview item, with its times shown in loc.
func adminTaskPayload(st worker.Status, loc *time.Location) adminTask {
	task := adminTask{
		Kind:            st.Kind,
		IntervalSeconds: int64(st.Interval / time.Second),
		Running:         st.Running,
		Pending:         st.Pending,
	}
	if !st.NextRunAt.IsZero() {
		task.NextRunAt = formatJobTime(&st.NextRunAt, loc)
	}
	if last := st.Last; last != nil {
		round := &adminTaskRound{
			Trigger:    last.Trigger,
			StartedAt:  formatJobTime(&last.StartedAt, loc),
			FinishedAt: formatJobTime(&last.FinishedAt, loc),
			Status:     jobs.StatusSucceeded,
			Summary:    map[string]int64(last.Summary),
		}
		if round.Summary == nil {
			round.Summary = map[string]int64{}
		}
		if last.Err != nil {
			round.Status = jobs.StatusFailed
			round.Error = last.Err.Error()
		}
		task.Last = round
	}
	return task
}

// adminTaskRun starts one round of a periodic task with the manual trigger and returns 202 with the task's overview
// item, without waiting for the round. The round applies the same rules as a timer round; it is always recorded in
// the job history.
func (s *Server) adminTaskRun(c *gin.Context) {
	actor, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	if s.tasks == nil {
		adminError(c, http.StatusServiceUnavailable, "unavailable")
		return
	}
	kind := strings.TrimSpace(c.Param("kind"))
	task, found := s.tasks.Get(kind)
	if !found {
		adminError(c, http.StatusNotFound, "invalid_task")
		return
	}
	if err := task.RunNow(); err != nil {
		switch {
		case errors.Is(err, worker.ErrBusy):
			adminError(c, http.StatusConflict, "already_running")
		case errors.Is(err, worker.ErrNotStarted):
			adminError(c, http.StatusServiceUnavailable, "unavailable")
		default:
			s.logger.Error("spa admin: run task failed", "kind", kind, "error", err)
			adminError(c, http.StatusInternalServerError, "failed")
		}
		return
	}
	s.auditor.Record(c.Request.Context(), store.AuditEntry{
		UserID: store.Ptr(actor.ID), Action: store.ActionJobRun,
		Detail: map[string]any{"kind": kind},
	})
	c.JSON(http.StatusAccepted, adminTaskPayload(task.Status(), auditLocation(actor)))
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
