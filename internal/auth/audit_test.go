package auth

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAuditorRecordIsBestEffort pins the one place the audit rule lives: a write lands in the table, a failed write is
// logged in English instead of returned, and a nil Auditor (audit not wired) is a silent no-op.
func TestAuditorRecordIsBestEffort(t *testing.T) {
	cases := []struct {
		name      string
		dropTable bool
		wantRows  int64
		wantLog   bool
	}{
		{name: "write lands", wantRows: 1},
		{name: "failed write is logged", dropTable: true, wantLog: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{})
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if err := db.AutoMigrate(store.AllModels()...); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			var logs bytes.Buffer
			a, err := NewAuditor(store.NewAuditStore(db), slog.New(slog.NewTextHandler(&logs, nil)))
			if err != nil {
				t.Fatalf("NewAuditor: %v", err)
			}
			if tc.dropTable {
				if err := db.Migrator().DropTable(&store.AuditLog{}); err != nil {
					t.Fatalf("drop audit table: %v", err)
				}
			}
			a.Record(context.Background(), store.AuditEntry{Action: store.ActionDeckUpdate})
			if got := strings.Contains(logs.String(), "write audit log failed"); got != tc.wantLog {
				t.Errorf("failure logged = %v, want %v (log %q)", got, tc.wantLog, logs.String())
			}
			if !tc.dropTable {
				var n int64
				db.Model(&store.AuditLog{}).Count(&n)
				if n != tc.wantRows {
					t.Errorf("audit rows = %d, want %d", n, tc.wantRows)
				}
			}
		})
	}

	var nilAuditor *Auditor
	nilAuditor.Record(context.Background(), store.AuditEntry{Action: store.ActionDeckUpdate})
}
