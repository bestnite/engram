package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestAuditStoreWriteRecordsOneRowPerMutation is an acceptance test:
// every change writes exactly one row, and the action string matches the constant exactly.
func TestAuditStoreWriteRecordsOneRowPerMutation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := NewUserStore(db)
			hash := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdA$aGFzaA"
			u := &User{Username: "alice", Email: "alice@example.com", DisplayName: "Alice",
				PasswordHash: &hash, Role: RoleUser, Status: StatusActive,
				Locale: "zh-CN", Timezone: "Asia/Shanghai"}
			if err := users.Create(ctx, u); err != nil {
				t.Fatalf("create user: %v", err)
			}

			audit := NewAuditStore(db)
			if err := audit.Write(ctx, AuditEntry{
				UserID:     Ptr(u.ID),
				Action:     ActionUserCreate,
				TargetType: "user",
				TargetID:   Ptr(u.ID),
				Detail:     map[string]any{"username": "alice", "role": RoleUser},
			}); err != nil {
				t.Fatalf("Write(user.create) error = %v", err)
			}
			// a second change must produce a second row instead of overwriting the previous one.
			if err := audit.Write(ctx, AuditEntry{
				UserID: Ptr(u.ID),
				Action: ActionUserLoginFailed,
				Detail: map[string]any{"username": "alice", "ip": "127.0.0.1"},
			}); err != nil {
				t.Fatalf("Write(user.login_failed) error = %v", err)
			}

			rows, err := audit.List(ctx, 10)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("audit rows = %d, want 2 (one per mutation)", len(rows))
			}
			// List is ordered by id descending: the last-written login_failed comes first.
			if rows[0].Action != ActionUserLoginFailed {
				t.Errorf("rows[0].Action = %q, want %q", rows[0].Action, ActionUserLoginFailed)
			}
			if rows[1].Action != ActionUserCreate {
				t.Errorf("rows[1].Action = %q, want %q", rows[1].Action, ActionUserCreate)
			}
			if n, err := audit.CountByAction(ctx, ActionUserCreate); err != nil || n != 1 {
				t.Errorf("CountByAction(user.create) = (%d, %v), want (1, nil)", n, err)
			}

			// user.create carries a target; detail_json must be parseable JSON.
			createRow := rows[1]
			if createRow.UserID == nil || *createRow.UserID != u.ID {
				t.Errorf("user.create UserID = %v, want %d", createRow.UserID, u.ID)
			}
			if createRow.TargetType == nil || *createRow.TargetType != "user" ||
				createRow.TargetID == nil || *createRow.TargetID != u.ID {
				t.Errorf("user.create target = (%v, %v), want (user, %d)",
					createRow.TargetType, createRow.TargetID, u.ID)
			}
			var detail map[string]any
			if createRow.DetailJSON == nil || json.Unmarshal([]byte(*createRow.DetailJSON), &detail) != nil {
				t.Fatalf("user.create detail_json = %v, want valid JSON", createRow.DetailJSON)
			}
			if detail["username"] != "alice" {
				t.Errorf("detail username = %v, want alice", detail["username"])
			}

			// login_failed without a user_id keeps that column NULL and is not faked with 0.
			failedRow := rows[0]
			if failedRow.TargetType != nil {
				t.Errorf("login_failed TargetType = %v, want NULL", *failedRow.TargetType)
			}
			noUser := AuditEntry{Action: ActionUserLoginFailed}
			if err := audit.Write(ctx, noUser); err != nil {
				t.Fatalf("Write without user error = %v", err)
			}
			latest, err := audit.List(ctx, 1)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if latest[0].UserID != nil {
				t.Errorf("system/anonymous audit UserID = %v, want NULL", *latest[0].UserID)
			}
			if latest[0].DetailJSON != nil {
				t.Errorf("audit without detail DetailJSON = %v, want NULL", *latest[0].DetailJSON)
			}
		})
	}
}

// TestAuditStoreRejectsEmptyAction asserts that an audit row without an action name is rejected: it cannot be searched.
func TestAuditStoreRejectsEmptyAction(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if err := NewAuditStore(db).Write(context.Background(), AuditEntry{}); err == nil {
				t.Error("Write with an empty action succeeded, want an error")
			}
		})
	}
}
