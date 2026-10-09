package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestRoleDenialWritesPermissionDeniedAudit 断言：卡组角色不足的 REST 写请求会留下一条
// permission.denied 审计行，target 指向卡组，detail 含 required_role / user_role / code。
//
// 这条能力历史上由 REST 死代码 requireDeckRole 与活路径 service.RequireDeckRole 各写一遍。
// 删死代码后必须仍由活路径覆盖；本用例就是那条能力的守卫（negative control 会临时改坏
// service 层的审计写入，本用例随即转红）。
func TestRoleDenialWritesPermissionDeniedAudit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "audit_owner", store.RoleUser)
	reader := seedUser(t, env.db, "audit_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	// reader 持有 write scope 的 key，但卡组角色只有 reader —— 两道门里第二道挡下。
	key := seedKey(t, env.keys, reader.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := "/api/v1/notes/" + note.PublicID

	status, raw := doJSON(t, router, http.MethodPatch, path, key.Plaintext,
		`{"kind":"basic","fields":{"front":"x","back":"y"}}`)
	if status != http.StatusForbidden {
		t.Fatalf("reader PATCH note status = %d, want 403 (body %s)", status, raw)
	}

	audit := store.NewAuditStore(env.db)
	if n, err := audit.CountByAction(ctx, store.ActionPermissionDenied); err != nil || n != 1 {
		t.Fatalf("permission.denied rows = (%d, %v), want (1, nil)", n, err)
	}
	var row store.AuditLog
	if err := env.db.Where("action = ?", store.ActionPermissionDenied).First(&row).Error; err != nil {
		t.Fatalf("load permission.denied row: %v", err)
	}
	if row.TargetType == nil || *row.TargetType != "deck" || row.TargetID == nil || *row.TargetID != deck.ID {
		t.Errorf("audit target = (%v, %v), want (deck, %d)", row.TargetType, row.TargetID, deck.ID)
	}
	if row.DetailJSON == nil {
		t.Fatal("audit detail_json is nil")
	}
	for _, want := range []string{
		`"required_role":"editor"`,
		`"user_role":"reader"`,
		`"code":"` + CodeInsufficientRole + `"`,
	} {
		if !strings.Contains(*row.DetailJSON, want) {
			t.Errorf("audit detail %s does not contain %s", *row.DetailJSON, want)
		}
	}
}
