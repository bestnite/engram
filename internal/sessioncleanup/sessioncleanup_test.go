package sessioncleanup

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// newSweepFixture 建一个真库（sqlite 文件，非内存）并返回会话 store，与 internal/reminder 的
// 测试同一套路。
func newSweepFixture(t *testing.T) *store.SessionStore {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return store.NewSessionStore(db)
}

// insertSession 写入一条会话行，行内时间戳相对当下计算。
func insertSession(t *testing.T, sessions *store.SessionStore, id string, expiresIn time.Duration) {
	t.Helper()
	if err := sessions.Create(context.Background(), &store.Session{
		ID:        id,
		UserID:    1,
		CSRFToken: "token-" + id,
		CreatedAt: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(expiresIn),
	}); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
}

// TestSweepRemovesOnlyExpiredSessions 断言一轮回收的语义：过期的删掉，未过期的原样保留。
//
// 先用 Start 触发（Lifecycle 的契约是「启动即跑一轮」），因此这条用例同时钉住了
// 「main 里 Start 之后回收真的发生了」这一接线前提。
func TestSweepRemovesOnlyExpiredSessions(t *testing.T) {
	sessions := newSweepFixture(t)
	insertSession(t, sessions, "expired-1", -48*time.Hour)
	insertSession(t, sessions, "expired-2", -time.Minute)
	insertSession(t, sessions, "live", 24*time.Hour)

	var logs bytes.Buffer
	lifecycle, err := New(Deps{
		Store:    sessions,
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle.Start(ctx)
	defer lifecycle.Stop()

	waitFor(t, 2*time.Second, func() bool {
		_, err := sessions.ByID(context.Background(), "expired-1")
		return err != nil
	})

	if _, err := sessions.ByID(context.Background(), "expired-2"); err == nil {
		t.Error("expired-2 should have been removed")
	}
	if _, err := sessions.ByID(context.Background(), "live"); err != nil {
		t.Errorf("live session must survive the sweep: %v", err)
	}
	if !strings.Contains(logs.String(), "expired sessions removed") {
		t.Errorf("sweep should log the removed count, got %q", logs.String())
	}
}

// TestSweepLogsNothingWhenNothingExpired 断言空转不产生日志噪音。
func TestSweepLogsNothingWhenNothingExpired(t *testing.T) {
	sessions := newSweepFixture(t)
	insertSession(t, sessions, "live", 24*time.Hour)

	var logs bytes.Buffer
	lifecycle, err := New(Deps{
		Store:    sessions,
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle.Start(ctx)
	lifecycle.Stop() // Stop 等当前轮跑完

	if strings.Contains(logs.String(), "expired sessions removed") {
		t.Errorf("an empty sweep must not log a removal: %q", logs.String())
	}
	if _, err := sessions.ByID(context.Background(), "live"); err != nil {
		t.Errorf("live session must survive: %v", err)
	}
}

// TestNewRequiresDeps 断言装配期的必填项校验（早失败胜过运行时 panic）。
func TestNewRequiresDeps(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if _, err := New(Deps{Logger: logger}); err == nil {
		t.Error("New() without Store should fail")
	}
	if _, err := New(Deps{Store: newSweepFixture(t)}); err == nil {
		t.Error("New() without Logger should fail")
	}
}

// waitFor 轮询条件直到成立或超时：用于等待后台 worker 跑完第一轮。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
