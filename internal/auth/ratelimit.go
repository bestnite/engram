package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// 登录限流的默认参数。延迟按基值 × 2^(失败次数-1) 递增并封顶 MaxDelay（
// 会话与安全约定）。
const (
	DefaultRateLimitBaseDelay = 200 * time.Millisecond
	DefaultRateLimitMaxDelay  = 5 * time.Second
	DefaultRateLimitWindow    = 15 * time.Minute
	// DefaultMaxAccountFailures 是一个账号在窗口内允许的失败次数，达到后直接拒绝到窗口结束。
	// 密码与 TOTP 第二步共用这个计数：6 位 TOTP 在每 15 分钟 10 次的速率下无法被穷举。
	DefaultMaxAccountFailures = 10
	// DefaultMaxIPFailures 是一个 IP 在窗口内允许的失败次数；比账号宽，因为同一出口 IP
	// 后面可能有多个正常用户。
	DefaultMaxIPFailures = 50
	// inflightTTL 是一次「已放行、尚未报告结果」的尝试占位的最长寿命。正常路径会在
	// RecordFailure / Reset / Release 里归还占位；中途出错返回的请求不归还，占位到期自动失效。
	inflightTTL = time.Minute
)

// ErrLoginLocked 表示账号或 IP 的失败次数已达上限，本次尝试被直接拒绝（不做认证）。
var ErrLoginLocked = errors.New("login temporarily locked")

// LockedError 携带解除锁定前还需等待的时长，供响应头 Retry-After 使用。
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string { return ErrLoginLocked.Error() }

// Is 让 errors.Is(err, ErrLoginLocked) 成立。
func (e *LockedError) Is(target error) bool { return target == ErrLoginLocked }

// LimiterConfig 是限流器配置；零值字段由 NewLoginLimiter 补默认值。
type LimiterConfig struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Window 是失败计数的保鲜期：超过它没有新的失败就当作已清零。
	Window time.Duration
	// MaxAccountFailures / MaxIPFailures 是两个维度的锁定阈值；<=0 时用默认值。
	MaxAccountFailures int
	MaxIPFailures      int
	// Now 可注入时钟；测试用它让窗口过期而不真实等待。
	Now func() time.Time
	// Sleep 可注入等待函数；测试注入记录器，避免真实睡眠拖慢测试。
	Sleep func(ctx context.Context, d time.Duration) error
}

// failureBucket 是单个键（账号或 IP）的失败计数与在途尝试数。
type failureBucket struct {
	count  int
	lastAt time.Time
	// inflight 是已经通过 Wait、还没报告结果的尝试数。锁定判断把它算进去：只看已记录的失败时，
	// 并发发出的一批请求会在第一条失败被记下之前全部通过检查，阈值就形同虚设。
	inflight   int
	inflightAt time.Time
}

// LoginLimiter 按账号与 IP 双维度记录登录失败：失败越多，后续尝试的延迟越长；
// 失败次数（含在途尝试）达到阈值后直接拒绝，直到窗口过去。
//
// 只有延迟没有锁定时，攻击者用并发就能把吞吐放大任意倍（每个请求都只是多睡几秒）。
// 锁定的代价是别人可以故意输错把某个账号锁住一个窗口（15 分钟）；对自托管实例这是
// 可接受的权衡，且成功登录会立即清零。
//
// 状态放在内存里：本服务单实例部署，不引 Redis；失败计数是短时防爆破数据，
// 进程重启即清零是可接受的权衡。
type LoginLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*failureBucket
	base       time.Duration
	max        time.Duration
	window     time.Duration
	maxAccount int
	maxIP      int
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
}

// NewLoginLimiter 构造限流器并补默认值；依赖都可注入，便于测试。
func NewLoginLimiter(cfg LimiterConfig) *LoginLimiter {
	l := &LoginLimiter{
		buckets:    make(map[string]*failureBucket),
		base:       cfg.BaseDelay,
		max:        cfg.MaxDelay,
		window:     cfg.Window,
		maxAccount: cfg.MaxAccountFailures,
		maxIP:      cfg.MaxIPFailures,
		now:        cfg.Now,
		sleep:      cfg.Sleep,
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
	if l.maxAccount <= 0 {
		l.maxAccount = DefaultMaxAccountFailures
	}
	if l.maxIP <= 0 {
		l.maxIP = DefaultMaxIPFailures
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

// keys 返回本次尝试涉及的两个键及其阈值；空账号名或空 IP 不建桶（它们的键没有区分度，
// 建了只会互相污染）。
func (l *LoginLimiter) keys(username, ip string) []struct {
	key   string
	limit int
} {
	out := make([]struct {
		key   string
		limit int
	}, 0, 2)
	if k := accountKey(username); k != "user:" {
		out = append(out, struct {
			key   string
			limit int
		}{k, l.maxAccount})
	}
	if k := ipKey(ip); k != "ip:" {
		out = append(out, struct {
			key   string
			limit int
		}{k, l.maxIP})
	}
	return out
}

// bucket 取某键的桶并按时间清理过期部分；不存在时返回 nil（调用方持锁）。
func (l *LoginLimiter) bucket(key string, now time.Time) *failureBucket {
	b, ok := l.buckets[key]
	if !ok {
		return nil
	}
	if b.count > 0 && (b.lastAt.IsZero() || now.Sub(b.lastAt) > l.window) {
		b.count = 0
	}
	if b.inflight > 0 && now.Sub(b.inflightAt) > inflightTTL {
		b.inflight = 0
	}
	if b.count == 0 && b.inflight == 0 {
		delete(l.buckets, key)
		return nil
	}
	return b
}

// activeCount 返回某键当前有效的失败次数（调用方持锁）。
func (l *LoginLimiter) activeCount(key string) int {
	if b := l.bucket(key, l.now()); b != nil {
		return b.count
	}
	return 0
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

// Wait 在认证前放行或拒绝一次尝试。
//
// 任一维度「已记录的失败 + 在途尝试」达到阈值时立即返回 *LockedError（errors.Is 为
// ErrLoginLocked），不等待、不占位；否则为两个维度各占一个在途位，再按已累计失败次数
// 递增等待，返回本次延迟（供日志与测试断言）。账号与 IP 两个维度取较大者：任一维度触发
// 限流都应减速，否则换账号刷同一 IP 就能绕过。
//
// 调用方在得到认证结果后必须调用 RecordFailure（失败）、Reset（成功）或 Release（不计成败，
// 例如第一因素通过、等待第二因素）之一，归还占位；忘记归还的占位在 inflightTTL 后失效。
func (l *LoginLimiter) Wait(ctx context.Context, username, ip string) (time.Duration, error) {
	l.mu.Lock()
	now := l.now()
	failures := 0
	var retry time.Duration
	locked := false
	keys := l.keys(username, ip)
	for _, k := range keys {
		b := l.bucket(k.key, now)
		if b == nil {
			continue
		}
		if b.count > failures {
			failures = b.count
		}
		if b.count+b.inflight >= k.limit {
			locked = true
			if r := l.window - now.Sub(b.lastAt); b.count > 0 && r > retry {
				retry = r
			}
			if r := inflightTTL - now.Sub(b.inflightAt); b.inflight > 0 && r > retry {
				retry = r
			}
		}
	}
	if locked {
		l.mu.Unlock()
		if retry <= 0 {
			retry = time.Second
		}
		return 0, &LockedError{RetryAfter: retry}
	}
	for _, k := range keys {
		b := l.buckets[k.key]
		if b == nil {
			b = &failureBucket{}
			l.buckets[k.key] = b
		}
		b.inflight++
		b.inflightAt = now
	}
	l.mu.Unlock()
	delay := l.delayFor(failures)
	if delay <= 0 {
		return 0, nil
	}
	if err := l.sleep(ctx, delay); err != nil {
		l.Release(username, ip)
		return delay, err
	}
	return delay, nil
}

// RecordFailure 记录一次失败并归还 Wait 的占位，返回该账号维度新的失败次数；
// 账号与 IP 两个维度同时累加。
func (l *LoginLimiter) RecordFailure(username, ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	count := 0
	account := accountKey(username)
	for _, k := range l.keys(username, ip) {
		b := l.bucket(k.key, now)
		if b == nil {
			b = &failureBucket{}
			l.buckets[k.key] = b
		}
		if b.inflight > 0 {
			b.inflight--
		}
		b.count++
		b.lastAt = now
		if k.key == account {
			count = b.count
		}
	}
	return count
}

// Release 归还 Wait 的占位但不记成败：第一因素通过、还要等第二因素时使用。
func (l *LoginLimiter) Release(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, k := range l.keys(username, ip) {
		if b := l.bucket(k.key, now); b != nil && b.inflight > 0 {
			b.inflight--
			if b.count == 0 && b.inflight == 0 {
				delete(l.buckets, k.key)
			}
		}
	}
}

// Reset 在登录成功后清零该账号与 IP 的失败计数与占位（验收：成功后重置）。
func (l *LoginLimiter) Reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, accountKey(username))
	delete(l.buckets, ipKey(ip))
}

// 匿名入口限流的默认参数：/forgot-password 与 /register 这类登录前入口
// 会被匿名轰炸式触发发信，按 IP 与目标邮箱双维度做固定窗口限流。阈值由 nite 拍板：
// 每个维度 5 次 / 15 分钟。
const (
	DefaultAnonRateLimit  = 5
	DefaultAnonRateWindow = 15 * time.Minute
)

// anonBucket 是固定窗口内一个键（IP 或邮箱）的请求计数。
type anonBucket struct {
	count int
	start time.Time
}

// AnonymousLimiter 对匿名入口按「请求方 IP」与「目标邮箱」双维度做固定窗口计数，
// 任一维度在一个窗口内超过上限即拒绝。
//
// 与 LoginLimiter 同属一类形态：状态放内存（单实例部署），时钟可注入以便
// 测试断言窗口过期而不真实等待。区别在于这里不看结果对不对，只按请求次数封顶——因为
// 注册与找回密码是合法请求也可能高频的入口，无法用「失败一次记一次」来衡量滥用。
type AnonymousLimiter struct {
	mu      sync.Mutex
	buckets map[string]*anonBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

// NewAnonymousLimiter 构造限流器并补默认值；now 可注入，nil 时用 UTC 挂钟。
func NewAnonymousLimiter(limit int, window time.Duration, now func() time.Time) *AnonymousLimiter {
	l := &AnonymousLimiter{
		buckets: make(map[string]*anonBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
	if l.limit <= 0 {
		l.limit = DefaultAnonRateLimit
	}
	if l.window <= 0 {
		l.window = DefaultAnonRateWindow
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	return l
}

// allowLocked 记一次某维度的请求并报告是否仍在配额内（调用方持锁）。
// 空值不建桶：没有区分度的键只会互相污染（与 RecordFailure 的空键处理一致）。
func (l *AnonymousLimiter) allowLocked(prefix, value string, now time.Time) bool {
	if value == "" {
		return true
	}
	key := prefix + value
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &anonBucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= l.limit
}

// Allow 记一次匿名入口请求并报告是否放行：IP 与目标邮箱任一超限都拒绝。
//
// 两个维度都会计数（与固定窗口限流器的惯例一致）：一旦某个维度进入超限状态，在窗口过期
// 前它都会继续被拒。邮箱先按小写去空白归一，避免大小写差异绕过邮箱维度的计数。
func (l *AnonymousLimiter) Allow(ip, email string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	okIP := l.allowLocked("ip:", strings.TrimSpace(ip), now)
	okEmail := l.allowLocked("email:", strings.ToLower(strings.TrimSpace(email)), now)
	return okIP && okEmail
}
