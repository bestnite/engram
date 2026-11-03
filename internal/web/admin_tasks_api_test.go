package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
)

// This file covers the periodic task endpoints of the admin jobs page: the overview, the manual run and its refusals,
// and the history fields the job list gained for periodic rows.

// blockingTask is a periodic worker whose rounds wait on release, so a test can hold a round open.
type blockingTask struct {
	lifecycle *worker.Lifecycle
	entered   chan struct{}
	release   chan struct{}
}

func newBlockingTask(t *testing.T, kind string, summary worker.Summary, err error) *blockingTask {
	t.Helper()
	bt := &blockingTask{entered: make(chan struct{}, 8), release: make(chan struct{}, 8)}
	l, nerr := worker.New(worker.Config{
		Kind: kind,
		RunOnce: func(ctx context.Context) (worker.Summary, error) {
			bt.entered <- struct{}{}
			select {
			case <-bt.release:
			case <-ctx.Done():
			}
			return summary, err
		},
		Logger:      discardLogger(),
		FailMessage: "test task failed",
		Interval:    time.Hour,
	})
	if nerr != nil {
		t.Fatalf("worker.New: %v", nerr)
	}
	bt.lifecycle = l
	return bt
}

// finishRound lets the current round finish and waits until the lifecycle reports it idle.
func (bt *blockingTask) finishRound(t *testing.T) {
	t.Helper()
	bt.release <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for bt.lifecycle.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("round did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

// taskServer builds the admin test server with a registry that records into its jobs table, holding a retention
// task whose startup round did nothing and a digest task whose startup round failed.
func taskServer(t *testing.T) (*Server, *blockingTask, *blockingTask, []*http.Cookie, string, func() []store.Job) {
	t.Helper()
	srv, db, _, cookies, csrf := newNotesServer(t)
	reg := worker.NewRegistry(jobs.RoundRecorder(jobs.NewStore(db), discardLogger()))
	retention := newBlockingTask(t, worker.KindRetention, worker.Summary{"sessions": 0}, nil)
	digest := newBlockingTask(t, worker.KindDigest, nil, errors.New("load digest candidates: boom"))
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() {
			cancel()
			retention.lifecycle.Stop()
			digest.lifecycle.Stop()
		})
	})
	for _, bt := range []*blockingTask{digest, retention} {
		if err := reg.Add(bt.lifecycle); err != nil {
			t.Fatalf("Add: %v", err)
		}
		bt.lifecycle.Start(ctx)
		<-bt.entered
		bt.finishRound(t)
	}
	srv.tasks = reg
	rows := func() []store.Job {
		var out []store.Job
		if err := db.Order("id").Find(&out).Error; err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		return out
	}
	return srv, retention, digest, cookies, csrf, rows
}

// TestAdminTasksOverview: every registered task is listed in kind order with its latest round, including a round
// that did nothing, and a failed round carries its error.
func TestAdminTasksOverview(t *testing.T) {
	srv, _, _, cookies, _, _ := taskServer(t)
	rec := getJSON(t, srv, "/api/v1/admin/tasks", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tasks = %d (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got adminTasksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(got.Tasks) != 2 || got.Tasks[0].Kind != worker.KindRetention || got.Tasks[1].Kind != worker.KindDigest {
		t.Fatalf("tasks = %+v, want retention then digest", got.Tasks)
	}
	idle := got.Tasks[0]
	if idle.IntervalSeconds != 3600 || idle.Running || idle.Pending || idle.NextRunAt == "" {
		t.Fatalf("retention task = %+v", idle)
	}
	if idle.Last == nil || idle.Last.Status != jobs.StatusSucceeded || idle.Last.Trigger != worker.TriggerAuto ||
		idle.Last.Summary == nil || idle.Last.Summary["sessions"] != 0 || idle.Last.StartedAt == "" || idle.Last.FinishedAt == "" {
		t.Fatalf("retention last round = %+v, want the no-op auto round", idle.Last)
	}
	failed := got.Tasks[1].Last
	if failed == nil || failed.Status != jobs.StatusFailed || failed.Error != "load digest candidates: boom" || failed.Summary == nil {
		t.Fatalf("digest last round = %+v, want the failure with an empty summary", failed)
	}
}

// TestAdminTasksOverviewWithoutRegistry: no registry wired is an empty overview, not an error.
func TestAdminTasksOverviewWithoutRegistry(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	rec := getJSON(t, srv, "/api/v1/admin/tasks", cookies, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"tasks":[]}` {
		t.Fatalf("GET tasks without registry = %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdminTaskRun covers the manual run: 202 with an audit row and a recorded history row even though the round
// did nothing, 409 while that round runs, 404 for an unknown or unregistered kind, 503 without a registry or after
// the worker stopped, 403 for a non-admin and 403 without a CSRF token.
func TestAdminTaskRun(t *testing.T) {
	srv, retention, _, cookies, csrf, rows := taskServer(t)
	// The startup rounds: the no-op retention round left no row, the failed digest round left one.
	before := rows()
	if len(before) != 1 || before[0].Kind != worker.KindDigest || before[0].Status != jobs.StatusFailed {
		t.Fatalf("rows after startup = %+v, want only the failed digest round", before)
	}

	rec := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, cookies, csrf)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run = %d (body %s), want 202", rec.Code, snippet(rec.Body.String()))
	}
	var accepted adminTask
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if accepted.Kind != worker.KindRetention || !(accepted.Pending || accepted.Running) {
		t.Fatalf("run response = %+v, want the retention task pending or running", accepted)
	}
	<-retention.entered
	// While the manual round runs a second request is refused.
	busy := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, cookies, csrf)
	assertAdminCode(t, busy, http.StatusConflict, "already_running")
	// The overview reports the running round.
	var mid adminTasksResponse
	if err := json.Unmarshal(getJSON(t, srv, "/api/v1/admin/tasks", cookies, nil).Body.Bytes(), &mid); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if !mid.Tasks[0].Running {
		t.Fatalf("retention task = %+v, want running", mid.Tasks[0])
	}
	retention.finishRound(t)

	var after []store.Job
	deadline := time.Now().Add(2 * time.Second)
	for {
		after = rows()
		if len(after) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(after) != 2 {
		t.Fatalf("rows after the manual round = %d, want 2", len(after))
	}
	manual := after[1]
	if manual.Kind != worker.KindRetention || manual.Trigger == nil || *manual.Trigger != worker.TriggerManual || manual.Status != jobs.StatusSucceeded {
		t.Fatalf("manual round row = %+v", manual)
	}
	var audits int64
	srv.db.Model(&store.AuditLog{}).Where("action = ? AND detail_json LIKE ?", store.ActionJobRun, `%"kind":"retention"%`).Count(&audits)
	if audits != 1 {
		t.Fatalf("job.run audit rows = %d, want 1", audits)
	}

	for _, kind := range []string{"nope", worker.KindReminder, jobs.KindOptimize} {
		rec := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/"+kind+"/run", map[string]any{}, cookies, csrf)
		assertAdminCode(t, rec, http.StatusNotFound, "invalid_task")
	}

	noCSRF := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, cookies, "")
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("run without CSRF = %d, want 403", noCSRF.Code)
	}
	_, strangerCookies, strangerCSRF := createUserAndLogin(t, srv, srv.db, "task_stranger")
	stranger := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, strangerCookies, strangerCSRF)
	if stranger.Code != http.StatusForbidden {
		t.Fatalf("non-admin run = %d, want 403", stranger.Code)
	}
	if rec := getJSON(t, srv, "/api/v1/admin/tasks", strangerCookies, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin overview = %d, want 403", rec.Code)
	}

	retention.lifecycle.Stop()
	stopped := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, cookies, csrf)
	assertAdminCode(t, stopped, http.StatusServiceUnavailable, "unavailable")

	srv.tasks = nil
	none := postJSONWithCSRF(t, srv, "/api/v1/admin/tasks/retention/run", map[string]any{}, cookies, csrf)
	assertAdminCode(t, none, http.StatusServiceUnavailable, "unavailable")
}

// TestAdminJobsHistoryFields: the kind filter, its 400, the trigger of a NULL-trigger optimizer row, the duration,
// and the summary of a periodic row next to the hidden result of an optimizer row.
func TestAdminJobsHistoryFields(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	started := time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC)
	finished := started.Add(2500 * time.Millisecond)
	legacy := store.Job{Kind: jobs.KindOptimize, Status: jobs.StatusSucceeded, ResultJSON: store.Ptr(`{"weights":[1,2]}`),
		CreatedAt: started, StartedAt: &started, FinishedAt: &finished}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed optimizer row: %v", err)
	}
	if err := jobs.NewStore(db).CreateRound(context.Background(), worker.Round{
		Kind: worker.KindMediaGC, Trigger: worker.TriggerAuto, StartedAt: started, FinishedAt: finished,
		Summary: worker.Summary{"removed": 3},
	}); err != nil {
		t.Fatalf("CreateRound: %v", err)
	}
	queued := store.Job{Kind: jobs.KindOptimize, Trigger: store.Ptr(jobs.TriggerUser), Status: jobs.StatusQueued, CreatedAt: finished}
	if err := db.Create(&queued).Error; err != nil {
		t.Fatalf("seed queued row: %v", err)
	}

	list := func(query string) adminJobsResponse {
		t.Helper()
		rec := getJSON(t, srv, "/api/v1/admin/jobs"+query, cookies, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET jobs%s = %d (body %s)", query, rec.Code, snippet(rec.Body.String()))
		}
		var got adminJobsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode jobs: %v", err)
		}
		return got
	}

	all := list("")
	if all.Total != 3 || len(all.Jobs) != 3 {
		t.Fatalf("all jobs = %+v", all)
	}
	byID := map[string]adminJob{}
	for _, j := range all.Jobs {
		byID[j.ID] = j
	}
	old := byID[legacy.PublicID]
	if old.Trigger != jobs.TriggerUser || old.Summary != nil || old.DurationMS == nil || *old.DurationMS != 2500 {
		t.Fatalf("legacy optimizer row = %+v, want trigger user, no summary, 2500 ms", old)
	}
	pending := byID[queued.PublicID]
	if pending.DurationMS != nil || !pending.CanCancel {
		t.Fatalf("queued row = %+v, want no duration and cancellable", pending)
	}

	media := list("?kind=media_gc")
	if media.Total != 1 || len(media.Jobs) != 1 {
		t.Fatalf("media_gc jobs = %+v", media)
	}
	if r := media.Jobs[0]; r.Kind != worker.KindMediaGC || r.Trigger != worker.TriggerAuto || r.Summary["removed"] != 3 || r.CanCancel {
		t.Fatalf("media_gc row = %+v", r)
	}
	if opt := list("?kind=optimize"); opt.Total != 2 {
		t.Fatalf("optimize jobs total = %d, want 2", opt.Total)
	}

	bad := getJSON(t, srv, "/api/v1/admin/jobs?kind=nope", cookies, nil)
	assertAdminCode(t, bad, http.StatusBadRequest, "invalid_kind")
}

// assertAdminCode asserts an admin API error response's status and stable code.
func assertAdminCode(t *testing.T, rec interface {
	Result() *http.Response
}, status int, code string) {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != status || body.Error.Code != code {
		t.Fatalf("response = %d %q, want %d %q", res.StatusCode, body.Error.Code, status, code)
	}
}

// TestAdminJobTimesFollowAdminTimezone: the history and the task overview show their times in the viewing admin's
// timezone, the way the audit and API key pages do, and an unknown or empty timezone falls back to UTC.
func TestAdminJobTimesFollowAdminTimezone(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		tz   string
		want string
	}{
		{"UTC", "2026-01-02 03:04:05"},
		{"Asia/Shanghai", "2026-01-02 11:04:05"},
		// The local day is the day before the UTC one.
		{"America/New_York", "2026-01-01 22:04:05"},
		{"", "2026-01-02 03:04:05"},
		{"Mars/Phobos", "2026-01-02 03:04:05"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			srv, retention, _, cookies, _, _ := taskServer(t)
			if err := srv.db.Model(&store.User{}).Where("1 = 1").Update("timezone", tc.tz).Error; err != nil {
				t.Fatalf("set timezone: %v", err)
			}
			loc := auditLocation(&store.User{Timezone: tc.tz})
			finished := created.Add(time.Minute)
			if err := srv.db.Create(&store.Job{
				Kind: worker.KindRetention, Trigger: store.Ptr(worker.TriggerManual), Status: jobs.StatusSucceeded,
				CreatedAt: created, StartedAt: &created, FinishedAt: &finished,
			}).Error; err != nil {
				t.Fatalf("create job: %v", err)
			}

			var history adminJobsResponse
			if err := json.Unmarshal(getJSON(t, srv, "/api/v1/admin/jobs?kind="+worker.KindRetention, cookies, nil).Body.Bytes(), &history); err != nil {
				t.Fatalf("decode jobs: %v", err)
			}
			if len(history.Jobs) != 1 {
				t.Fatalf("jobs = %+v, want the seeded row", history.Jobs)
			}
			if got := history.Jobs[0]; got.CreatedAt != tc.want || got.StartedAt != tc.want || got.FinishedAt != finished.In(loc).Format("2006-01-02 15:04:05") {
				t.Fatalf("job times = %q/%q/%q, want created and started %q", got.CreatedAt, got.StartedAt, got.FinishedAt, tc.want)
			}

			var overview adminTasksResponse
			if err := json.Unmarshal(getJSON(t, srv, "/api/v1/admin/tasks", cookies, nil).Body.Bytes(), &overview); err != nil {
				t.Fatalf("decode tasks: %v", err)
			}
			st := retention.lifecycle.Status()
			got := overview.Tasks[0]
			if want := st.NextRunAt.In(loc).Format("2006-01-02 15:04:05"); got.NextRunAt != want {
				t.Errorf("next_run_at = %q, want %q", got.NextRunAt, want)
			}
			if want := st.Last.FinishedAt.In(loc).Format("2006-01-02 15:04:05"); got.Last == nil || got.Last.FinishedAt != want {
				t.Errorf("last round = %+v, want finished_at %q", got.Last, want)
			}
		})
	}
}
