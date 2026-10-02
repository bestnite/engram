package mail

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// recordingSender 是一个可注入的投递替身：按顺序返回预置错误，用于验证重试与永久失败，
// 不依赖任何真实 SMTP 服务。
type recordingSender struct {
	mu   sync.Mutex
	errs []error // 第 i 次 Send 返回 errs[i]；耗尽后返回 nil
	n    int
	sent []Message
	// block 非 nil 时 Send 阻塞到它被关闭，用于证明"不在请求路径同步发信"。
	block chan struct{}
}

func (s *recordingSender) Send(_ context.Context, _ SMTPConfig, m Message) error {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	if s.n < len(s.errs) {
		err := s.errs[s.n]
		s.n++
		return err
	}
	s.n++
	return nil
}

func (s *recordingSender) Test(_ context.Context, _ SMTPConfig) error { return nil }

func (s *recordingSender) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// waitFor 轮询 cond 直到为真或超时；超时即 t.Fatal，避免测试挂死。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// fastRetry 是测试用的退避策略：短延迟，让重试在毫秒级完成。
func fastRetry(maxAttempts int) RetryPolicy {
	return RetryPolicy{MaxAttempts: maxAttempts, BaseDelay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond}
}

// configuredSettings 往库里写一份可用的 SMTP 配置（指向 addr 的 host/port 与 from）。
func configuredSettings(t *testing.T, db *gorm.DB, hostPort string, from string) {
	t.Helper()
	h, p := splitHostPort(t, hostPort)
	setSettings(t, db, map[string]string{
		SettingKeySMTPHost: h, SettingKeySMTPPort: p, SettingKeySMTPFrom: from,
		SettingKeySMTPTLSMode: TLSModeNone,
	})
}
