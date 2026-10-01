package jobs

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/store"
)

// Store 封装 jobs 表；模型复用 internal/store.Job，避免重复定义（AGENTS.md §2.4：
// 一个模型同时服务业务、GORM 与 JSON）。
type Store struct {
	db *gorm.DB
}

// NewStore 构造作业存储。
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// CreateQueued 写入一行 queued 作业并返回它。
func (s *Store) CreateQueued(ctx context.Context, kind string, targetID *uint64, at time.Time) (*store.Job, error) {
	job := store.Job{
		Kind:      kind,
		TargetID:  targetID,
		Status:    StatusQueued,
		CreatedAt: at.UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&job).Error; err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return &job, nil
}

// ByID 按主键读取作业。
func (s *Store) ByID(ctx context.Context, id uint64) (*store.Job, error) {
	var job store.Job
	if err := s.db.WithContext(ctx).First(&job, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("load job %d: %w", id, err)
	}
	return &job, nil
}

// Active 返回当前未完成的作业（queued 或 running）；没有时返回 (nil, nil)。
// 单并发判定依赖它：只要存在未完成作业，新入队就必须被拒绝。
func (s *Store) Active(ctx context.Context) (*store.Job, error) {
	var job store.Job
	err := s.db.WithContext(ctx).
		Where("status IN ?", []string{StatusQueued, StatusRunning}).
		Order("id DESC").First(&job).Error
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("find active job: %w", err)
	}
	return &job, nil
}

// MarkRunning 把作业置为 running 并记录 started_at。
func (s *Store) MarkRunning(ctx context.Context, id uint64, at time.Time) error {
	return s.update(ctx, id, map[string]any{
		"status":     StatusRunning,
		"started_at": at.UTC(),
	})
}

// SetStage 更新阶段级进度。
func (s *Store) SetStage(ctx context.Context, id uint64, stage string) error {
	return s.update(ctx, id, map[string]any{"stage": stage})
}

// FinishSucceeded 把作业置为 succeeded：写 finished_at、日志尾巴与结果，并清空 error。
func (s *Store) FinishSucceeded(ctx context.Context, id uint64, at time.Time, resultJSON, tail string) error {
	updates := map[string]any{
		"status":      StatusSucceeded,
		"finished_at": at.UTC(),
		"log_tail":    nullableString(tail),
		"result_json": nullableString(resultJSON),
		"error":       nil,
	}
	return s.update(ctx, id, updates)
}

// FinishFailed 把作业置为 failed：写 finished_at、失败原因与日志尾巴。
func (s *Store) FinishFailed(ctx context.Context, id uint64, at time.Time, msg, tail string) error {
	updates := map[string]any{
		"status":      StatusFailed,
		"finished_at": at.UTC(),
		"error":       msg,
		"log_tail":    nullableString(tail),
	}
	return s.update(ctx, id, updates)
}

// update 按 id 更新若干列；行不存在时返回错误，避免静默丢失状态。
func (s *Store) update(ctx context.Context, id uint64, updates map[string]any) error {
	res := s.db.WithContext(ctx).Model(&store.Job{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update job %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("update job %d: %w", id, gorm.ErrRecordNotFound)
	}
	return nil
}

// nullableString 把空串转成 nil，让可空 TEXT 列存 NULL 而不是空字符串（DESIGN.md §2.2）。
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
