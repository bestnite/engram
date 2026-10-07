package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// jobFileSize 返回文件当前字节数；不存在时返回 0。
func jobFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// loadJob 直接按主键读一行 jobs，供测试断言状态与失败原因。
func loadJob(t *testing.T, db *gorm.DB, id uint64) *store.Job {
	t.Helper()
	var job store.Job
	if err := db.First(&job, "id = ?", id).Error; err != nil {
		t.Fatalf("load job %d: %v", id, err)
	}
	return &job
}

// waitJobStatus 轮询到作业到达目标状态；超时报错。
func waitJobStatus(t *testing.T, db *gorm.DB, id uint64, want string, limit time.Duration) *store.Job {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		job := loadJob(t, db, id)
		if job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job := loadJob(t, db, id)
	t.Fatalf("job %d did not reach %q within %s (last %q)", id, want, limit, job.Status)
	return nil
}

// TestAdminJobsCancelRunningJob 是 M6-6 的验收：管理员在作业页看到运行中的作业，
// 提交取消后状态变为 failed 且带取消原因；同时验证列表渲染与审计留痕。
//
// 用一个真实的长时间子进程让作业停在 running；取消后用 marker 文件证明进程确实被杀，
// 而不是只改了状态字段。
func TestAdminJobsCancelRunningJob(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	// GET /admin/jobs 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 作业列表渲染。
	srv.spa = nil

	marker := filepath.Join(t.TempDir(), "ticks")
	var calls int32
	builder := func(_ context.Context, _ *store.Job, _ jobs.Reporter) (jobs.Command, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return jobs.Command{
				Name: "/bin/sh",
				Args: []string{"-c", "while true; do echo tick >> " + marker + "; sleep 0.05; done"},
			}, nil
		}
		return jobs.Command{Name: "/bin/true"}, nil
	}
	runner, err := jobs.New(jobs.Deps{DB: db, Timeout: time.Minute, Command: builder})
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runner.Start(ctx)
	srv.jobRunner = runner

	job, err := runner.Enqueue(ctx, jobs.KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// 等作业进入 running 且子进程确实产出。
	deadline := time.Now().Add(5 * time.Second)
	for {
		cur := loadJob(t, db, job.ID)
		if cur.Status == jobs.StatusRunning && jobFileSize(marker) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d never reached running with live output (status %q, marker=%d)", job.ID, cur.Status, jobFileSize(marker))
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 列表页应展示该运行中的作业（默认语言 zh-CN）。
	listRec := getWithCookies(t, srv, "/admin/jobs", cookies)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /admin/jobs = %d, want 200 (body %s)", listRec.Code, snippet(listRec.Body.String()))
	}
	if body := listRec.Body.String(); !strings.Contains(body, "运行中") {
		t.Errorf("jobs page does not show the running job: %s", snippet(body))
	}

	// 取消：带 CSRF 与确认字段。
	resp := adminPost(t, srv, "/admin/jobs/"+strconv.FormatUint(job.ID, 10)+"/cancel",
		url.Values{"confirm": {"1"}}, cookies, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST cancel = %d, want 303", resp.StatusCode)
	}

	after := waitJobStatus(t, db, job.ID, jobs.StatusFailed, 3*time.Second)
	if after.Status != jobs.StatusFailed {
		t.Errorf("cancelled job status = %q, want %q", after.Status, jobs.StatusFailed)
	}
	if after.Error == nil || *after.Error != jobs.CancelReason {
		t.Errorf("cancelled job error = %v, want %q", after.Error, jobs.CancelReason)
	}

	// 进程确实被杀：取消后 marker 不再增长。
	size := jobFileSize(marker)
	time.Sleep(400 * time.Millisecond)
	if grew := jobFileSize(marker); grew != size {
		t.Errorf("subprocess survived the cancel: marker grew %d -> %d", size, grew)
	}

	// 取消后新作业能入队并跑完（单并发门已释放）。
	second, err := runner.Enqueue(ctx, jobs.KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue after cancel: %v", err)
	}
	waitJobStatus(t, db, second.ID, jobs.StatusSucceeded, 5*time.Second)

	// 页面应显示失败原因（取消原因）。
	rec2 := getWithCookies(t, srv, "/admin/jobs", cookies)
	if !strings.Contains(rec2.Body.String(), jobs.CancelReason) {
		t.Errorf("jobs page does not show the cancellation reason %q", jobs.CancelReason)
	}

	// 审计留痕：取消动作必须写入 job.cancel。
	n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionJobCancel)
	if err != nil {
		t.Fatalf("CountByAction(job.cancel): %v", err)
	}
	if n == 0 {
		t.Error("no job.cancel audit row written for the cancel action")
	}
}
