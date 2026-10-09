package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminAuditFilterAcceptsPublicTargetID 断言审计检索的 target_id 过滤认对外 id：
// 管理面板列表显示的就是对外 id，把同一个 id 粘回筛选框必须能筛出对应的行。
func TestAdminAuditFilterAcceptsPublicTargetID(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	preset := store.NewPreset(ownerID, "AuditFilterPreset")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: "audit-filter-deck", Visibility: "private",
		PresetID: preset.ID, CreatedAt: time.Now().UTC()}
	if err := store.NewDeckStore(db).Create(ctx, &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := db.Where("1 = 1").Delete(&store.AuditLog{}).Error; err != nil {
		t.Fatalf("clear audit log: %v", err)
	}
	deckType := "deck"
	row := store.AuditLog{UserID: store.Ptr(ownerID), Action: "deck.grant",
		TargetType: &deckType, TargetID: store.Ptr(deck.ID)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed audit row: %v", err)
	}

	rec := getJSON(t, srv, "/api/v1/admin/audit?target_type=deck&target_id="+deck.PublicID, cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET audit by public target id = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got adminAuditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if got.Total != 1 || len(got.Rows) != 1 {
		t.Fatalf("audit by public target id = %d/%d, want 1/1", got.Total, len(got.Rows))
	}
	if got.Rows[0].Target == nil || got.Rows[0].Target.ID == nil || *got.Rows[0].Target.ID != deck.PublicID {
		t.Errorf("audit row target = %+v, want public id %s", got.Rows[0].Target, deck.PublicID)
	}

	// 未知对外 id：给稳定 notice 码，而不是静默退化成「不过滤」。
	rec = getJSON(t, srv, "/api/v1/admin/audit?target_type=deck&target_id=01a11f66-0000-7000-8000-000000000000", cookies, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if got.Total != 0 || got.Notice != "invalid_target" {
		t.Errorf("unknown public target id = total %d notice %q, want 0 / invalid_target", got.Total, got.Notice)
	}
}
