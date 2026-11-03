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

// TestFailedStatusWriteEndsTheRound covers the state a full database volume produces: reads still
// work, writes fail. The row whose "sent" update failed is still due, so a drain that carried on
// would select the same batch again and deliver it again without end. The round must stop after the
// failed write and leave the row for the next tick.
func TestFailedStatusWriteEndsTheRound(t *testing.T) {
	db := testDB(t)
	for i := 0; i < 2; i++ {
		if err := store.EnqueueOutboxMessage(context.Background(), db, &store.OutboxMessage{
			To: "a@example.com", Type: "invite", Subject: "stuck", HeadersJSON: "{}",
		}, time.Now().UTC()); err != nil {
			t.Fatalf("EnqueueOutboxMessage: %v", err)
		}
	}
	// The injected failure is the status write, not the send: the mail goes out and its outcome
	// cannot be recorded.
	if err := db.Exec(`CREATE TRIGGER mail_outbox_reject_sent BEFORE UPDATE OF status ON mail_outbox
		WHEN NEW.status = 'sent' BEGIN SELECT RAISE(ABORT, 'injected status write failure'); END`).Error; err != nil {
		t.Fatalf("install failing status-write trigger: %v", err)
	}
	sender := &recordingSender{}
	ob := NewOutbox(Deps{DB: db, Sender: sender, Retry: fastRetry(3), PollInterval: 5 * time.Millisecond, BatchSize: 2})
	configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	var drainErr error
	go func() {
		defer close(done)
		_, drainErr = ob.drain(ctx)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatalf("drain did not return after the status write failed; Send calls = %d", sender.calls())
	}
	if got := sender.calls(); got > 2 {
		t.Errorf("Send calls = %d, want at most one batch (2)", got)
	}
	// The stopped round is a failed round, so the history records it.
	if drainErr == nil {
		t.Error("drain returned no error after the status write failed")
	}
	var pending int64
	if err := db.Model(&store.OutboxMessage{}).Where("status = ?", store.OutboxStatusPending).Count(&pending).Error; err != nil {
		t.Fatalf("count pending rows: %v", err)
	}
	if pending != 2 {
		t.Errorf("pending rows = %d, want 2 (a failed status write leaves its row for the next tick)", pending)
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

// TestDrainSummaryCountsOutcomes: a delivery round reports each written outcome under its summary key, and a round
// with nothing due reports no work, so the history skips it.
func TestDrainSummaryCountsOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		maxAttempts int
		errs        []error
		want        map[string]int64
	}{
		{"nothing due", 3, nil, map[string]int64{}},
		{"one sent, one rescheduled", 3, []error{errors.New("421 try later")}, map[string]int64{"sent": 1, "retried": 1}},
		{"one sent, one failed for good", 1, []error{errors.New("550 no such user")}, map[string]int64{"sent": 1, "failed": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := testDB(t)
			if tc.errs != nil {
				for i := 0; i < 2; i++ {
					if err := store.EnqueueOutboxMessage(context.Background(), db, &store.OutboxMessage{
						To: "a@example.com", Type: "invite", Subject: "summary", HeadersJSON: "{}",
					}, time.Now().UTC()); err != nil {
						t.Fatalf("EnqueueOutboxMessage: %v", err)
					}
				}
			}
			ob := NewOutbox(Deps{DB: db, Sender: &recordingSender{errs: tc.errs}, Retry: fastRetry(tc.maxAttempts)})
			configuredSettings(t, db, "127.0.0.1:587", "no-reply@example.com")
			summary, err := ob.drain(context.Background())
			if err != nil {
				t.Fatalf("drain: %v", err)
			}
			for _, key := range []string{"sent", "failed", "retried"} {
				if summary[key] != tc.want[key] {
					t.Errorf("summary[%s] = %d, want %d (summary %v)", key, summary[key], tc.want[key], summary)
				}
			}
			if summary.DidWork() != (len(tc.want) > 0) {
				t.Errorf("DidWork() = %v for summary %v", summary.DidWork(), summary)
			}
		})
	}
}
