package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestAuditStoreWriteRecordsOneRowPerMutation 是 M1-10 的验收测试：
// 每次变更写入恰好一行，且 action 字符串与常量完全一致。
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
			// 第二次变更必须产生第二行，而不是覆盖上一次。
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
			// List 按 id 倒序：最后写入的 login_failed 在最前。
			if rows[0].Action != ActionUserLoginFailed {
				t.Errorf("rows[0].Action = %q, want %q", rows[0].Action, ActionUserLoginFailed)
			}
			if rows[1].Action != ActionUserCreate {
				t.Errorf("rows[1].Action = %q, want %q", rows[1].Action, ActionUserCreate)
			}
			if n, err := audit.CountByAction(ctx, ActionUserCreate); err != nil || n != 1 {
				t.Errorf("CountByAction(user.create) = (%d, %v), want (1, nil)", n, err)
			}

			// user.create 带目标；detail_json 必须是可解析的 JSON。
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

			// login_failed 没有 user_id 时该列保持 NULL，不被 0 冒充。
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

// TestAuditStoreRejectsEmptyAction 断言没有动作名的审计行被拒绝：它无法被检索。
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
