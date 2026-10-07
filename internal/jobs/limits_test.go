//go:build unix

// 只在 Unix 上编译：复用 jobs_test.go / optimizer_wiring_test.go 里 unix-only 的测试脚手架
// （newWiringDB、okBuilder 等）。本文件覆盖两个健壮性缺陷：MarkRunning 失败必须把作业
// 标成 failed（而不是留在 queued 阻塞队列），以及适配器权重文件的读取必须有尺寸上限。

package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// errMarkRunningInjected 是测试注入的 MarkRunning 故障原因，供断言失败文本里出现它。
var errMarkRunningInjected = errors.New("injected mark running failure")

// injectMarkRunningFailure 注册一个 GORM update 回调：命中「把作业置为 running」的那次更新时
// 注入错误。Store 是具体类型（Deps.Store 不能注入假实现），所以只能在 DB 层制造故障；
// 只拦截 status=running 的更新，FinishFailed 等其它更新照常执行。
func injectMarkRunningFailure(db *gorm.DB) {
	db.Callback().Update().Before("gorm:update").Register("jobs_test:fail_mark_running", func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		if status, ok := updates["status"].(string); ok && status == StatusRunning {
			tx.AddError(errMarkRunningInjected)
		}
	})
}

// runJobWithFailingMarkRunning 建真实 SQLite 库、注入 MarkRunning 故障并同步执行一次作业，
// 返回 Runner、Store 与作业 id。刻意不 Start worker：直接调 execute，避免与后台 goroutine 抢时序。
func runJobWithFailingMarkRunning(t *testing.T) (*Runner, *Store, uint64) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	st := NewStore(db)
	injectMarkRunningFailure(db)

	runner, err := New(Deps{DB: db, Store: st, Timeout: time.Second, Command: okBuilder})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	runner.execute(ctx, job)
	return runner, st, job.ID
}

// TestMarkRunningFailureMarksJobFailed 覆盖第一个缺陷的落库部分：MarkRunning 失败时作业必须
// 进入 failed 终态，并带上点名原因的英文错误文本；修前它只记日志就 return，作业停在 queued。
func TestMarkRunningFailureMarksJobFailed(t *testing.T) {
	_, st, id := runJobWithFailingMarkRunning(t)

	got, err := st.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("job %d status = %q after MarkRunning failed, want %q", id, got.Status, StatusFailed)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "mark job running") {
		t.Fatalf("job %d error = %v, want it to name the failed step (%q)", id, got.Error, "mark job running")
	}
	if got.Error == nil || !strings.Contains(*got.Error, errMarkRunningInjected.Error()) {
		t.Fatalf("job %d error = %v, want it to carry the underlying cause %q", id, got.Error, errMarkRunningInjected.Error())
	}
	if got.FinishedAt == nil {
		t.Fatalf("job %d failed without finished_at", id)
	}
	t.Logf("job %d -> status=%s error=%q finished=%v", id, got.Status, *got.Error, got.FinishedAt != nil)
}

// TestMarkRunningFailureUnblocksQueue 覆盖第一个缺陷的阻塞部分：MarkRunning 失败留下的
// queued 行会被 Store.Active 当作在途作业，让后续 Enqueue 永久 409。修后该行已 failed，
// 新作业必须能入队。修前 Enqueue 返回 ErrAlreadyRunning（HTTP 409）。
func TestMarkRunningFailureUnblocksQueue(t *testing.T) {
	runner, st, id := runJobWithFailingMarkRunning(t)
	ctx := context.Background()

	// 先确认前置条件：那次作业确实因 MarkRunning 失败而终态化（否则本用例失去意义）。
	got, err := st.ByID(ctx, id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("precondition failed: job %d status = %q, want %q", id, got.Status, StatusFailed)
	}

	if _, err := runner.Enqueue(ctx, KindOptimize, nil); errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Enqueue after a MarkRunning failure = %v; the stuck job still blocks new jobs (HTTP 409)", err)
	} else if err != nil {
		t.Fatalf("Enqueue after a MarkRunning failure: %v", err)
	}
	t.Logf("job %d terminal (%s); a fresh Enqueue is no longer refused", id, got.Status)
}

// testWeightsLimit 是测试视角的权重文件上限（1 MiB）。刻意写死而不引用实现里的常量：
// 这样「未实现上限」时测试仍能编译，从而给出有意义的红灯而不是编译错误。
const testWeightsLimit = 1 << 20

// paddedWeightsJSON 构造 21 个合法权重、总字节数恰为 totalBytes 的 JSON（用空白字符填充）。
// 结果是合法 JSON，因此「修前的整体读取 + 解码」会成功——红灯来自缺少尺寸上限，而非解码失败。
func paddedWeightsJSON(t *testing.T, totalBytes int) []byte {
	t.Helper()
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < optimizerWeightCount; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("0.5")
	}
	b.WriteByte(']')
	body := b.String()
	if len(body) > totalBytes {
		t.Fatalf("totalBytes %d is smaller than the minimal weights JSON (%d bytes)", totalBytes, len(body))
	}
	pad := strings.Repeat(" ", totalBytes-len(body))
	return []byte("[" + pad + body[1:])
}

// newCompleteTestOptimizer 构造一个 root 指向临时目录的 Optimizer，并预置作业目录与
// review-log.jsonl（Complete 在读完权重后会统计它，缺失会让正例提前失败）。
func newCompleteTestOptimizer(t *testing.T, job *store.Job) *Optimizer {
	t.Helper()
	db, _ := newWiringDB(t)
	opt, err := NewOptimizer(OptimizerDeps{DB: db})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	opt.root = t.TempDir()
	dir := opt.jobDir(job)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review-log.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write review log: %v", err)
	}
	return opt
}

// TestCompleteRejectsOversizedWeights 覆盖第二个缺陷：适配器写出的 weights.json 超过上限时，
// Complete 必须报错（点名上限），而不能把整个文件读进内存。文件是合法 JSON，所以修前会被
// 整体读取并成功解码——红灯正是「无尺寸上限」本身。
func TestCompleteRejectsOversizedWeights(t *testing.T) {
	job := &store.Job{ID: 11}
	opt := newCompleteTestOptimizer(t, job)
	dir := opt.jobDir(job)

	raw := paddedWeightsJSON(t, testWeightsLimit+1)
	if len(raw) <= testWeightsLimit {
		t.Fatalf("test fixture is %d bytes, want > %d", len(raw), testWeightsLimit)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.json"), raw, 0o600); err != nil {
		t.Fatalf("write oversized weights: %v", err)
	}

	_, err := opt.Complete(context.Background(), job, Command{}, "")
	if err == nil {
		t.Fatalf("Complete accepted a %d-byte weights.json and returned nil; it must refuse files over %d bytes", len(raw), testWeightsLimit)
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Complete error = %q, want it to mention the size limit", err)
	}
	// 上限值必须出现在错误里，运维才能判断是哪个约束被触发。
	if !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("Complete error = %q, want it to name the %d-byte limit", err, testWeightsLimit)
	}
	t.Logf("oversized weights (%d bytes) rejected: %v", len(raw), err)
}

// TestCompleteAcceptsWeightsAtLimit 是缺陷 2 的正例（也是边界）：文件恰为上限字节数时仍可读、
// 21 个权重被正确解析。它保证加上的上限不会误伤正常输出，并锁死「上限是闭区间」的语义。
func TestCompleteAcceptsWeightsAtLimit(t *testing.T) {
	job := &store.Job{ID: 12}
	opt := newCompleteTestOptimizer(t, job)
	dir := opt.jobDir(job)

	raw := paddedWeightsJSON(t, testWeightsLimit)
	if len(raw) != testWeightsLimit {
		t.Fatalf("fixture is %d bytes, want exactly %d", len(raw), testWeightsLimit)
	}
	if err := os.WriteFile(filepath.Join(dir, "weights.json"), raw, 0o600); err != nil {
		t.Fatalf("write weights: %v", err)
	}

	result, err := opt.Complete(context.Background(), job, Command{}, "")
	if err != nil {
		t.Fatalf("Complete rejected a weights.json of exactly %d bytes: %v", testWeightsLimit, err)
	}
	if len(result.Weights) != optimizerWeightCount {
		t.Fatalf("Complete parsed %d weights, want %d", len(result.Weights), optimizerWeightCount)
	}
	t.Logf("weights.json of exactly %d bytes accepted: parsed %d weights", len(raw), len(result.Weights))
}
