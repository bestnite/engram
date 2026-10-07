// Package sessioncleanup 周期回收已过期的会话行。
//
// 为什么需要它：会话行在「登出 / 改密 / 撤销」时只被写成撤销标记（Revoke 写 revoked_at），
// 从不删行；「过期」也只是读路径上的判定（Manager.Middleware 校验 expires_at 后当匿名处理）。
// 因此没有这一层回收，sessions 表只会随使用增长、永不回落。
//
// 骨架与 internal/reminder、internal/digest 同构：复用 worker.Lifecycle 的起停与 tick 循环，
// 本包只提供「扫什么」。
package sessioncleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.nite07.com/nite/engram/internal/worker"
)

// DefaultInterval 是默认的回收间隔（1 小时）。
//
// 选取理由：回收的对象是**已经过期**的行，晚删一会儿没有任何用户可感知的后果（读路径本来就
// 拒绝它们），所以不必像 reminder/digest 那样十分钟一轮——纯清理任务按小时取，少制造无谓的
// 周期性写入。expires_at 上有索引，单次删除的代价与表大小无关。
const DefaultInterval = time.Hour

// Store 是本包需要的最小能力，由 *store.SessionStore 满足；抽成接口便于测试注入。
type Store interface {
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// Deps 是 New 的构造参数。
type Deps struct {
	// Store 提供删除能力。必填。
	Store Store
	// Logger 记录每轮结果。必填。
	Logger *slog.Logger
	// Interval 是回收间隔；<=0 时用 DefaultInterval。
	Interval time.Duration
}

// New 构造回收 worker；不启动，需再调用 Start。
func New(deps Deps) (*worker.Lifecycle, error) {
	if deps.Store == nil {
		return nil, errors.New("sessioncleanup: Deps.Store is required")
	}
	if deps.Logger == nil {
		return nil, errors.New("sessioncleanup: Deps.Logger is required")
	}
	return worker.New(worker.Config{
		Interval: deps.Interval,
		Logger:   deps.Logger,
		RunOnce: func(ctx context.Context) error {
			removed, err := deps.Store.DeleteExpired(ctx, time.Now())
			if err != nil {
				return fmt.Errorf("delete expired sessions: %w", err)
			}
			// 只在真的删了东西时记一行：正常的每小时空转不该产生日志噪音。
			if removed > 0 {
				deps.Logger.Info("expired sessions removed", "count", removed)
			}
			return nil
		},
		FailMessage: "session cleanup: sweep failed",
	})
}
