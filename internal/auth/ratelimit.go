package auth

import (
	"context"
	"strings"
	"sync"
	"time"
)

// 登录限流的默认参数。延迟按 基值 × 2^(失败次数-1) 递增并封顶 MaxDelay（ROADMAP.md M1-9、
// DESIGN.md §4.3、§11）。
const (
	DefaultRateLimitBaseDelay = 200 * time.Millisecond
	DefaultRateLimitMaxDelay  = 5 * time.Second
	DefaultRateLimitWindow    = 15 * time.Minute
)

// LimiterConfig 是限流器配置；零值字段由 NewLoginLimiter 补默认值。
type LimiterConfig struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Window 是失败计数的保鲜期：超过它没有新的失败就当作已清零。
	Window time.Duration
	// Now 可注入时钟；测试用它让窗口过期而不真实等待。
	Now func() time.Time
	// Sleep 可注入等待函数；测试注入记录器，避免真实睡眠拖慢测试。
	Sleep func(ctx context.Context, d time.Duration) error
}

// failureBucket 是单个键（账号或 IP）的失败计数。
type failureBucket struct {
	count  int
	lastAt time.Time
}

// LoginLimiter 按账号与 IP 双维度记录登录失败，并对后续尝试施加递增延迟。
//
// 状态放在内存里：本服务单实例部署（DESIGN.md §11），不引 Redis；失败计数是短时防爆破数据，
// 进程重启即清零是可接受的权衡。
type LoginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*failureBucket
	base    time.Duration
	max     time.Duration
	window  time.Duration
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration) error
}

// NewLoginLimiter 构造限流器并补默认值；依赖都可注入，便于测试。
func NewLoginLimiter(cfg LimiterConfig) *LoginLimiter {
	l := &LoginLimiter{
		buckets: make(map[string]*failureBucket),
		base:    cfg.BaseDelay,
		max:     cfg.MaxDelay,
		window:  cfg.Window,
		now:     cfg.Now,
		sleep:   cfg.Sleep,
	}
	if l.base <= 0 {
		l.base = DefaultRateLimitBaseDelay
	}
	if l.max <= 0 {
		l.max = DefaultRateLimitMaxDelay
	}
	if l.max < l.base {
		l.max = l.base
	}
	if l.window <= 0 {
		l.window = DefaultRateLimitWindow
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	if l.sleep == nil {
		l.sleep = sleepContext
	}
	return l
}

// sleepContext 是可被 ctx 取消的等待；请求断开时应立刻返回而不是继续占着连接。
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func accountKey(username string) string {
	return "user:" + strings.ToLower(strings.TrimSpace(username))
}
func ipKey(ip string) string { return "ip:" + strings.TrimSpace(ip) }

// activeCount 返回某键当前有效的失败次数；过期的桶顺手删除（调用方持锁）。
func (l *LoginLimiter) activeCount(key string) int {
	b, ok := l.buckets[key]
	if !ok {
		return 0
	}
	if b.lastAt.IsZero() || l.now().Sub(b.lastAt) > l.window {
		delete(l.buckets, key)
		return 0
	}
	return b.count
}

// delayFor 把失败次数换算成等待时长：0 次不等待，之后按 2 的幂递增并封顶。
func (l *LoginLimiter) delayFor(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	d := l.base
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= l.max {
			return l.max
		}
	}
	if d > l.max {
		d = l.max
	}
	return d
}

// Wait 在认证前等待与已累计失败次数成正比的延迟，并返回本次延迟（供日志与测试断言）。
// 账号与 IP 两个维度取较大者：任一维度触发限流都应减速，否则换账号刷同一 IP 就能绕过。
func (l *LoginLimiter) Wait(ctx context.Context, username, ip string) (time.Duration, error) {
	l.mu.Lock()
	failures := l.activeCount(accountKey(username))
	if c := l.activeCount(ipKey(ip)); c > failures {
		failures = c
	}
	l.mu.Unlock()
	delay := l.delayFor(failures)
	if delay <= 0 {
		return 0, nil
	}
	if err := l.sleep(ctx, delay); err != nil {
		return delay, err
	}
	return delay, nil
}

// RecordFailure 记录一次失败，返回该账号维度新的失败次数；账号与 IP 两个维度同时累加。
// 空账号名或空 IP 不建桶（它们的键没有区分度，建了只会互相污染）。
func (l *LoginLimiter) RecordFailure(username, ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	account := accountKey(username)
	count := 0
	for _, key := range []string{account, ipKey(ip)} {
		if key == "user:" || key == "ip:" {
			continue
		}
		b, ok := l.buckets[key]
		if !ok || b.lastAt.IsZero() || now.Sub(b.lastAt) > l.window {
			b = &failureBucket{}
			l.buckets[key] = b
		}
		b.count++
		b.lastAt = now
		if key == account {
			count = b.count
		}
	}
	return count
}

// Reset 在登录成功后清零该账号与 IP 的失败计数（M1-9 验收：成功后重置）。
func (l *LoginLimiter) Reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, accountKey(username))
	delete(l.buckets, ipKey(ip))
}
