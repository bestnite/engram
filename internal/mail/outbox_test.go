package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/engram/internal/store"
)

// TestEnqueueDoesNotSendInRequestPath 是 M1-17 的核心约束之一：Enqueue 只写队列表。
// 用一个阻塞在 gate 上的替身证明 Send 尚未完成时 Enqueue 已经返回。
func TestEnqueueDoesNotSendInRequestPath(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{block: make(chan struct{})}
	ob, err := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	if err := ob.Enqueue(context.Background(), Message{To: "a@example.com", Subject: "queued"}); err != nil {
		t.Fatalf("Enqueue() error = %v, want nil", err)
	}
	// 此刻替身还阻塞着，说明 Enqueue 没有同步发信。
	if sender.calls() != 0 {
		t.Errorf("Send was called synchronously during Enqueue (calls=%d)", sender.calls())
	}
	// 队列里已有一行 pending。
	rows, err := store.DueOutboxMessages(context.Background(), db, time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("DueOutboxMessages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued rows = %d, want 1", len(rows))
	}
	close(sender.block)
}

// TestWorkerDeliversQueuedMessage 覆盖正常投递：Enqueue 后 worker 把邮件发出并标记 sent。
func TestWorkerDeliversQueuedMessage(t *testing.T) {
	db := testDB(t)
	f := newFakeSMTP(t)
	ob, err := NewOutbox(Deps{
		DB: db, Sender: SMTPSender{}, Retry: fastRetry(3), PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}
	configuredSettings(t, db, f.addr, "no-reply@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ob.Start(ctx)
	defer ob.Stop()

	if err := ob.Enqueue(ctx, Message{
		To: "learner@example.com", Type: "invite", Subject: "Your invite",
		TextBody: "hello there",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v, want nil", err)
	}

	waitFor(t, 5*time.Second, "message delivered", func() bool {
		var row store.OutboxMessage
		if err := db.First(&row).Error; err != nil {
			return false
		}
		return row.Status == store.OutboxStatusSent
	})

	msgs := f.received()
	if len(msgs) != 1 {
		t.Fatalf("fake SMTP received %d messages, want 1", len(msgs))
	}
	body := msgs[0]
	if !contains(body, "Subject: Your invite") || !contains(body, "hello there") {
		t.Errorf("delivered body is missing headers/body:\n%s", body)
	}
	if !contains(body, "To: learner@example.com") {
		t.Errorf("delivered body is missing the recipient:\n%s", body)
	}
}

// TestTransientFailureIsRetriedAndLastErrorVisible 是 M1-17 的验收点：
// 瞬时发信失败会被重试，最后一次错误与尝试次数可从管理面板读到（OutboxSummaryOf）。
func TestTransientFailureIsRetriedAndLastErrorVisible(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{errs: []error{
		errors.New("421 4.3.0 temporary local problem"),
		errors.New("421 4.3.0 temporary local problem"),
	}} // 第三次成功
	ob, err := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(5), PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ob.Start(ctx)
	defer ob.Stop()

	// Enqueue 必须成功返回：发信失败不得让触发它的操作失败。
	if err := ob.Enqueue(ctx, Message{To: "a@example.com", Subject: "retry me"}); err != nil {
		t.Fatalf("Enqueue() error = %v, want nil (a send failure must not fail the trigger)", err)
	}

	waitFor(t, 5*time.Second, "message delivered after retries", func() bool {
		var row store.OutboxMessage
		if err := db.First(&row).Error; err != nil {
			return false
		}
		return row.Status == store.OutboxStatusSent
	})
	if got := sender.calls(); got != 3 {
		t.Errorf("Send calls = %d, want 3 (two failures then success)", got)
	}

	// 最后一次错误与尝试次数在 outbox 读数里可见（管理面板据此展示）。
	summary, err := store.OutboxSummaryOf(ctx, db)
	if err != nil {
		t.Fatalf("OutboxSummaryOf: %v", err)
	}
	if summary.Sent != 1 {
		t.Errorf("summary.Sent = %d, want 1", summary.Sent)
	}
	if summary.LastError != "421 4.3.0 temporary local problem" {
		t.Errorf("summary.LastError = %q, want the server error text", summary.LastError)
	}
	if summary.LastAttempts != 2 {
		t.Errorf("summary.LastAttempts = %d, want 2 (the last failure before success)", summary.LastAttempts)
	}
}

// TestPermanentFailureStopsAfterMaxAttempts 覆盖重试耗尽：标记为 failed 并保留最后一次错误。
func TestPermanentFailureStopsAfterMaxAttempts(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{errs: []error{
		errors.New("451 temp A"), errors.New("451 temp B"), errors.New("451 temp C"),
	}}
	ob, err := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ob.Start(ctx)
	defer ob.Stop()

	if err := ob.Enqueue(ctx, Message{To: "a@example.com", Subject: "doomed"}); err != nil {
		t.Fatalf("Enqueue() error = %v, want nil", err)
	}
	waitFor(t, 5*time.Second, "message marked failed", func() bool {
		var row store.OutboxMessage
		if err := db.First(&row).Error; err != nil {
			return false
		}
		return row.Status == store.OutboxStatusFailed
	})
	var row store.OutboxMessage
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Attempts != 3 {
		t.Errorf("attempts = %d, want 3", row.Attempts)
	}
	if row.LastError == nil || *row.LastError != "451 temp C" {
		t.Errorf("last_error = %v, want the final server error", row.LastError)
	}
	// 永久失败也要能被面板读到。
	summary, err := store.OutboxSummaryOf(ctx, db)
	if err != nil {
		t.Fatalf("OutboxSummaryOf: %v", err)
	}
	if summary.Failed != 1 || summary.LastError != "451 temp C" {
		t.Errorf("summary = %+v, want 1 failed with the final error text", summary)
	}
}

// TestUnconfiguredWorkerLeavesQueueIntact 证明配置缺失不会被当成投递失败：
// 已有 pending 邮件在未配置时保持 pending，等配置补齐再发，而不是被烧成 failed。
func TestUnconfiguredWorkerLeavesQueueIntact(t *testing.T) {
	db := testDB(t)
	// 直接写一行 pending（模拟"曾经配置过、后来配置被清掉"的积压）。
	if err := store.EnqueueOutboxMessage(context.Background(), db, &store.OutboxMessage{
		To: "a@example.com", Type: "invite", Subject: "old", HeadersJSON: "{}",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueOutboxMessage: %v", err)
	}
	ob, err := NewOutbox(Deps{DB: db, Sender: &recordingSender{}, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewOutbox: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ob.Start(ctx)
	defer ob.Stop()
	time.Sleep(60 * time.Millisecond)

	var row store.OutboxMessage
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Status != store.OutboxStatusPending {
		t.Errorf("status = %q, want pending (unconfigured must not burn the queue)", row.Status)
	}
}

// TestRetryPolicyBackoff 覆盖退避时长的计算：指数增长且被上限截断。
func TestRetryPolicyBackoff(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	want := map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 5: 10 * time.Second}
	for attempts, w := range want {
		if got := p.backoff(attempts); got != w {
			t.Errorf("backoff(%d) = %s, want %s", attempts, got, w)
		}
	}
}
