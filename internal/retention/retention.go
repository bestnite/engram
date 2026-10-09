// Package retention 周期回收各类「只增不减」的历史行。
//
// 为什么需要它：几类行只在读路径上被判过期／过时，从不被删除——会话行在登出/撤销时只写
// revoked_at、过期也只在 Manager.Middleware 里判定；邮箱验证/重置/退订令牌用完或过期后仍留在
// action_tokens；过期邀请留在 invites；登录指纹按「每个用户见过的每个 (IP, 设备) 一行」累积。
// 没有这一层回收，这些表只会随使用增长、永不回落。
//
// 骨架与 internal/reminder、internal/digest 同构：复用 worker.Lifecycle 的起停与 tick 循环，
// 本包只提供「扫什么、保留多久」。
//
// 保留期是策略，集中在下面这组常量里，一处可改：
//
//   - 会话：到期即删（读路径已不再接受，留着唯一的作用是占空间）。
//   - 令牌（验证/重置/退订）：到期后再留 ActionTokenGrace，方便排查「链接失效」类反馈。
//   - 邀请：到期即删；expires_at 为 NULL 的邀请永不过期，绝不删。
//   - 登录指纹：最后出现后再留 FingerprintRetention——删早了会把老设备误判成新设备并触发提醒。
//   - 卡组共享邀请：到期即删（读路径也判过期，删掉只是让表不再增长）。
//   - 媒体：没有任何 note 引用的媒体先记下孤立时刻，孤立超过 MediaOrphanGrace 后连同文件删除。
//     宽限期给「编辑时删掉图片又改回来」与「上传后还没插进卡片」留出窗口。
//
// **API key 刻意不自动删**：它是用户可见、可自行删除的资产，且数量与用户数同阶（不是增长源）；
// 自动删掉一张「已过期但还在列表里」的 key 属于替用户做决定。
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.nite07.com/nite/engram/internal/worker"
)

const (
	// DefaultInterval 是默认回收间隔（1 小时）。
	//
	// 选取理由：回收的对象都是**已经过期/过时**的行，晚删一会儿没有任何用户可感知的后果，
	// 所以不必像 reminder/digest 那样十分钟一轮。相关时间列都有索引，单次删除与表大小无关。
	DefaultInterval = time.Hour
	// ActionTokenGrace 是令牌过期后仍保留的时长（30 天）。
	ActionTokenGrace = 30 * 24 * time.Hour
	// FingerprintRetention 是登录指纹在最后一次出现后保留的时长（90 天）。
	FingerprintRetention = 90 * 24 * time.Hour
	// MediaOrphanGrace 是媒体变成无人引用之后仍保留的时长（7 天）。
	MediaOrphanGrace = 7 * 24 * time.Hour
)

// Expirer 是「按时间删除过期行」的最小能力，由 store 的会话/令牌/邀请实现。
type Expirer interface {
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// StalePurger 是「按最后出现时间删除」的最小能力，由登录指纹 store 实现。
type StalePurger interface {
	DeleteStale(ctx context.Context, before time.Time) (int64, error)
}

// MediaCollector 是「回收孤立媒体」的最小能力，由 internal/media 的 Store 实现。
type MediaCollector interface {
	CollectGarbage(ctx context.Context, now time.Time, grace time.Duration) (int64, error)
}

// Deps 是 New 的构造参数；各类存储都必填（缺一项就少回收一类，属于装配错误，早失败更好）。
type Deps struct {
	Sessions     Expirer
	ActionTokens Expirer
	Invites      Expirer
	ShareInvites Expirer
	Fingerprints StalePurger
	Media        MediaCollector
	Logger       *slog.Logger
	// Interval 是回收间隔；<=0 时用 DefaultInterval。
	Interval time.Duration
}

// New 构造回收 worker；不启动，需再调用 Start。
func New(deps Deps) (*worker.Lifecycle, error) {
	switch {
	case deps.Sessions == nil:
		return nil, errors.New("retention: Deps.Sessions is required")
	case deps.ActionTokens == nil:
		return nil, errors.New("retention: Deps.ActionTokens is required")
	case deps.Invites == nil:
		return nil, errors.New("retention: Deps.Invites is required")
	case deps.ShareInvites == nil:
		return nil, errors.New("retention: Deps.ShareInvites is required")
	case deps.Fingerprints == nil:
		return nil, errors.New("retention: Deps.Fingerprints is required")
	case deps.Media == nil:
		return nil, errors.New("retention: Deps.Media is required")
	case deps.Logger == nil:
		return nil, errors.New("retention: Deps.Logger is required")
	}
	return worker.New(worker.Config{
		Interval: deps.Interval,
		Logger:   deps.Logger,
		RunOnce:  func(ctx context.Context) error { return sweep(ctx, deps) },
		// 单轮失败由 worker 记日志后继续：某一类删不掉不该拖住其他三类。
		FailMessage: "retention: sweep failed",
	})
}

// sweep 跑一轮回收：各类互相独立，各自失败都记下来（用 errors.Join 一并返回），
// 已成功删除的行数照常上报，不因为另一类出错而丢弃日志。
func sweep(ctx context.Context, deps Deps) error {
	// **界定值必须是 UTC**：这几张表的时间列全部由写入方以 UTC 落库（auth.Manager.now、
	// ActionTokenService.now、邀请创建、指纹 Touch 都显式 .UTC()），而 SQLite 把时间当字符串
	// 比较、不认时区偏移——传本地时区的界定值会把窗口整体平移一个 UTC 偏移量（在 UTC+8 上实测
	// 会把还差一天才到期的行删掉）。这与 store/health.go、store/reminder.go 等处传 now.UTC()
	// 的既有约定一致。PostgreSQL 列是真时间类型，不受此影响。
	now := time.Now().UTC()
	targets := []struct {
		table  string
		before time.Time
		run    func(context.Context, time.Time) (int64, error)
	}{
		{"sessions", now, deps.Sessions.DeleteExpired},
		{"action_tokens", now.Add(-ActionTokenGrace), deps.ActionTokens.DeleteExpired},
		{"invites", now, deps.Invites.DeleteExpired},
		{"deck_share_invites", now, deps.ShareInvites.DeleteExpired},
		{"login_fingerprints", now.Add(-FingerprintRetention), deps.Fingerprints.DeleteStale},
	}

	var errs []error
	for _, t := range targets {
		removed, err := t.run(ctx, t.before)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.table, err))
			continue
		}
		// 只在真的删了东西时记一行：正常的每小时空转不该产生日志噪音。
		if removed > 0 {
			deps.Logger.Info("expired rows removed", "table", t.table, "count", removed)
		}
	}
	removed, err := deps.Media.CollectGarbage(ctx, now, MediaOrphanGrace)
	if err != nil {
		errs = append(errs, fmt.Errorf("media: %w", err))
	}
	if removed > 0 {
		deps.Logger.Info("orphaned media removed", "count", removed)
	}
	return errors.Join(errs...)
}
