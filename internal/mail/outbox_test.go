package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestEnqueueDoesNotSendInRequestPath is one of the core constraints: Enqueue only writes the queue
// table. A stand-in that blocks on a gate proves Enqueue has already returned while Send is not
// finished.
func TestEnqueueDoesNotSendInRequestPath(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{block: make(chan struct{})}
	ob := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	if err := ob.Enqueue(context.Background(), Message{To: "a@example.com", Subject: "queued"}); err != nil {
		t.Fatalf("Enqueue() error = %v, want nil", err)
	}
	// The stand-in is still blocked at this point, which shows Enqueue did not send synchronously.
	if sender.calls() != 0 {
		t.Errorf("Send was called synchronously during Enqueue (calls=%d)", sender.calls())
	}
	// The queue already has one pending row.
	rows, err := store.DueOutboxMessages(context.Background(), db, time.Now().UTC().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("DueOutboxMessages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("queued rows = %d, want 1", len(rows))
	}
	close(sender.block)
}

// TestWorkerDeliversQueuedMessage covers normal delivery: after Enqueue the worker sends the mail and
// marks it sent.
func TestWorkerDeliversQueuedMessage(t *testing.T) {
	db := testDB(t)
	f := newFakeSMTP(t)
	ob := NewOutbox(Deps{
		DB: db, Sender: SMTPSender{}, Retry: fastRetry(3), PollInterval: 10 * time.Millisecond,
	})
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

// TestTransientFailureIsRetriedAndLastErrorVisible is an acceptance point: a transient send failure
// is retried, and the last error and the attempt count can be read from the admin panel
// (OutboxSummaryOf).
func TestTransientFailureIsRetriedAndLastErrorVisible(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{errs: []error{
		errors.New("421 4.3.0 temporary local problem"),
		errors.New("421 4.3.0 temporary local problem"),
	}} // the third attempt succeeds
	ob := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(5), PollInterval: 5 * time.Millisecond})
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ob.Start(ctx)
	defer ob.Stop()

	// Enqueue must return successfully: a send failure must not fail the operation that triggered it.
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

	// The last error and the attempt count are visible in the outbox summary (what the admin panel shows).
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

// TestPermanentFailureStopsAfterMaxAttempts covers retry exhaustion: the mail is marked failed and
// the last error is kept.
func TestPermanentFailureStopsAfterMaxAttempts(t *testing.T) {
	db := testDB(t)
	sender := &recordingSender{errs: []error{
		errors.New("451 temp A"), errors.New("451 temp B"), errors.New("451 temp C"),
	}}
	ob := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
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
	// Permanent failures must be readable by the panel as well.
	summary, err := store.OutboxSummaryOf(ctx, db)
	if err != nil {
		t.Fatalf("OutboxSummaryOf: %v", err)
	}
	if summary.Failed != 1 || summary.LastError != "451 temp C" {
		t.Errorf("summary = %+v, want 1 failed with the final error text", summary)
	}
}

// TestUnconfiguredWorkerLeavesQueueIntact shows that a missing configuration is not treated as a
// delivery failure: existing pending mail stays pending while unconfigured and is sent once the
// configuration is complete, instead of being burned into failed.
func TestUnconfiguredWorkerLeavesQueueIntact(t *testing.T) {
	db := testDB(t)
	// Write one pending row directly (simulating a backlog from "it used to be configured and the
	// configuration was later cleared").
	if err := store.EnqueueOutboxMessage(context.Background(), db, &store.OutboxMessage{
		To: "a@example.com", Type: "invite", Subject: "old", HeadersJSON: "{}",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueOutboxMessage: %v", err)
	}
	ob := NewOutbox(Deps{DB: db, Sender: &recordingSender{}, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond})
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

// TestRetryPolicyBackoff covers the backoff duration computation: exponential growth capped at the
// maximum.
func TestRetryPolicyBackoff(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	want := map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 5: 10 * time.Second}
	for attempts, w := range want {
		if got := p.backoff(attempts); got != w {
			t.Errorf("backoff(%d) = %s, want %s", attempts, got, w)
		}
	}
}
