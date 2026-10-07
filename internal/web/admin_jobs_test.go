package web

import (
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

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
