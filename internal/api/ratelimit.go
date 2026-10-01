package api

import (
	"sync"
	"time"
)

// 按 key 限流的默认参数（DESIGN.md §7.2：默认 60 req/min，写入类更严）。
const (
	DefaultReadLimit  = 60
	DefaultWriteLimit = 30
	DefaultRateWindow = time.Minute
)

// rateBucket 是固定窗口内一个键的请求计数。
type rateBucket struct {
	count int
	start time.Time
}

// RateLimiter 是按 key 或用户计数的内存限流器。
//
// 状态放内存：本服务单实例部署（DESIGN.md §11），不引 Redis；限流是短时保护数据，
// 进程重启即清零是可接受的权衡。时钟可注入，测试因此不需要真实等待。
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rateBucket
	limit   int
	window  time.Duration
	now     func() time.Time
}

// NewRateLimiter 构造限流器；limit <= 0 时用 DefaultReadLimit，window <= 0 时用 DefaultRateWindow。
func NewRateLimiter(limit int, window time.Duration, now func() time.Time) *RateLimiter {
	l := &RateLimiter{
		buckets: make(map[string]*rateBucket),
		limit:   limit,
		window:  window,
		now:     now,
	}
	if l.limit <= 0 {
		l.limit = DefaultReadLimit
	}
	if l.window <= 0 {
		l.window = DefaultRateWindow
	}
	if l.now == nil {
		l.now = func() time.Time { return time.Now().UTC() }
	}
	return l
}

// Allow 记一次请求并返回是否仍在配额内（固定窗口：窗口过期即重新计数）。
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &rateBucket{count: 1, start: now}
		return true
	}
	b.count++
	return b.count <= l.limit
}
