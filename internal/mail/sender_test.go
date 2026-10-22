package mail

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// recordingSender is an injectable delivery stand-in: it returns preset errors in order, used to
// verify retries and permanent failure without depending on any real SMTP service.
type recordingSender struct {
	mu   sync.Mutex
	errs []error // the i-th Send returns errs[i]; once exhausted it returns nil
	n    int
	sent []Message
	// when block is non-nil, Send blocks until it is closed, proving "no synchronous send on the request path".
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

// waitFor polls cond until it is true or the timeout expires; a timeout is t.Fatal, so a test cannot
// hang.
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

// fastRetry is the backoff policy used in tests: short delays, so retries finish in milliseconds.
func fastRetry(maxAttempts int) RetryPolicy {
	return RetryPolicy{MaxAttempts: maxAttempts, BaseDelay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond}
}

// configuredSettings writes a usable SMTP configuration into the database (host/port pointing at addr,
// plus from).
func configuredSettings(t *testing.T, db *gorm.DB, hostPort string, from string) {
	t.Helper()
	h, p := splitHostPort(t, hostPort)
	setSettings(t, db, map[string]string{
		SettingKeySMTPHost: h, SettingKeySMTPPort: p, SettingKeySMTPFrom: from,
		SettingKeySMTPTLSMode: TLSModeNone,
	})
}
