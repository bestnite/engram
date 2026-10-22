package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openOutboxTestDB opens a temporary SQLite database that only migrates the outbox table, focusing on data access itself.
func openOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbox.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&OutboxMessage{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

// TestOutboxEnqueueForcesPendingState proves the write entry point forces pending/attempts=0, so callers cannot get the status wrong.
func TestOutboxEnqueueForcesPendingState(t *testing.T) {
	db := openOutboxTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// Dirty status fields are stuffed in on purpose; the write entry point must overwrite them.
	row := &OutboxMessage{
		To: "a@example.com", Type: "invite", Subject: "hi", HeadersJSON: "{}",
		Status: OutboxStatusSent, Attempts: 99,
	}
	if err := EnqueueOutboxMessage(ctx, db, row, now); err != nil {
		t.Fatalf("EnqueueOutboxMessage: %v", err)
	}
	if row.Status != OutboxStatusPending || row.Attempts != 0 || row.SentAt != nil {
		t.Errorf("enqueue did not force a clean pending row: %+v", row)
	}
	if row.ID == 0 {
		t.Error("enqueue did not assign an id")
	}
}

// TestDueOutboxMessagesHonorsNextAttemptAt proves the backoff: rows that are not yet due are not picked up.
func TestDueOutboxMessagesHonorsNextAttemptAt(t *testing.T) {
	db := openOutboxTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// One due, one in the future.
	due := &OutboxMessage{To: "due@example.com", Type: "invite", Subject: "due", HeadersJSON: "{}"}
	if err := EnqueueOutboxMessage(ctx, db, due, now); err != nil {
		t.Fatalf("enqueue due: %v", err)
	}
	later := &OutboxMessage{To: "later@example.com", Type: "invite", Subject: "later", HeadersJSON: "{}"}
	if err := EnqueueOutboxMessage(ctx, db, later, now); err != nil {
		t.Fatalf("enqueue later: %v", err)
	}
	if err := MarkOutboxRetry(ctx, db, later.ID, 1, now.Add(time.Hour), "temp", now); err != nil {
		t.Fatalf("MarkOutboxRetry: %v", err)
	}

	rows, err := DueOutboxMessages(ctx, db, now, 10)
	if err != nil {
		t.Fatalf("DueOutboxMessages: %v", err)
	}
	if len(rows) != 1 || rows[0].To != "due@example.com" {
		t.Fatalf("due rows = %+v, want only the due message", rows)
	}
}

// TestOutboxSummaryReportsCountsAndLastError covers the aggregation the admin panel reads:
// per-status counts plus the most recent error text and attempt count.
func TestOutboxSummaryReportsCountsAndLastError(t *testing.T) {
	db := openOutboxTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sent := &OutboxMessage{To: "s@example.com", Type: "invite", Subject: "s", HeadersJSON: "{}"}
	if err := EnqueueOutboxMessage(ctx, db, sent, now); err != nil {
		t.Fatalf("enqueue sent: %v", err)
	}
	if err := MarkOutboxSent(ctx, db, sent.ID, now); err != nil {
		t.Fatalf("MarkOutboxSent: %v", err)
	}
	pending := &OutboxMessage{To: "p@example.com", Type: "invite", Subject: "p", HeadersJSON: "{}"}
	if err := EnqueueOutboxMessage(ctx, db, pending, now); err != nil {
		t.Fatalf("enqueue pending: %v", err)
	}
	failed := &OutboxMessage{To: "f@example.com", Type: "invite", Subject: "f", HeadersJSON: "{}"}
	if err := EnqueueOutboxMessage(ctx, db, failed, now); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	if err := MarkOutboxFailed(ctx, db, failed.ID, 3, "550 no such user", now); err != nil {
		t.Fatalf("MarkOutboxFailed: %v", err)
	}

	summary, err := OutboxSummaryOf(ctx, db)
	if err != nil {
		t.Fatalf("OutboxSummaryOf: %v", err)
	}
	if summary.Sent != 1 || summary.Pending != 1 || summary.Failed != 1 {
		t.Errorf("counts = sent:%d pending:%d failed:%d, want 1/1/1", summary.Sent, summary.Pending, summary.Failed)
	}
	if summary.LastError != "550 no such user" {
		t.Errorf("LastError = %q, want the recorded text", summary.LastError)
	}
	if summary.LastAttempts != 3 {
		t.Errorf("LastAttempts = %d, want 3", summary.LastAttempts)
	}
}

// TestOutboxTableNameIsStable pins the table name so the pluralization rule cannot drift (AutoMigrate relies on it).
func TestOutboxTableNameIsStable(t *testing.T) {
	if got := (OutboxMessage{}).TableName(); got != "mail_outbox" {
		t.Errorf("OutboxMessage.TableName() = %q, want mail_outbox", got)
	}
	// It must be registered in AllModels, otherwise AutoMigrate silently misses the table.
	found := false
	for _, m := range AllModels() {
		if _, ok := m.(*OutboxMessage); ok {
			found = true
		}
	}
	if !found {
		t.Error("OutboxMessage is not registered in AllModels(); migrations would silently skip it")
	}
}
