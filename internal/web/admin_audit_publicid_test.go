package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminAuditFilterAcceptsPublicTargetID asserts that the target_id filter in audit search accepts public ids:
// the admin panel list shows public ids, so pasting the same id back into the filter box must select the matching row.
func TestAdminAuditFilterAcceptsPublicTargetID(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	preset := store.NewPreset(ownerID, "AuditFilterPreset")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: "audit-filter-deck",
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

	// Unknown public id: return a stable notice code instead of silently degrading to "no filter".
	rec = getJSON(t, srv, "/api/v1/admin/audit?target_type=deck&target_id=01a11f66-0000-7000-8000-000000000000", cookies, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if got.Total != 0 || got.Notice != "invalid_target" {
		t.Errorf("unknown public target id = total %d notice %q, want 0 / invalid_target", got.Total, got.Notice)
	}
}
