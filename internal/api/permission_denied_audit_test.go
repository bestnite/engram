package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestRoleDenialWritesPermissionDeniedAudit asserts: a REST write request with an insufficient deck role leaves one
// permission.denied audit row, with target pointing at the deck and detail carrying required_role / user_role / code.
//
// Historically this capability was written twice: in the REST dead code requireDeckRole and on the live path service.RequireDeckRole.
// After the dead code is deleted the live path must still cover it; this case is the guard for that capability (the negative control temporarily breaks
// the service layer's audit write and this case turns red at once).
func TestRoleDenialWritesPermissionDeniedAudit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	owner := seedUser(t, env.db, "audit_owner", store.RoleUser)
	reader := seedUser(t, env.db, "audit_reader", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, reader.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	// reader holds a key with write scope, but the deck role is only reader - the second of the two doors stops it.
	key := seedKey(t, env.keys, reader.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := "/api/v1/notes/" + note.PublicID

	status, raw := doJSON(t, router, http.MethodPatch, path, key.Plaintext,
		fmt.Sprintf(`{"note_type":%q,"fields":{"front":"x","back":"y"}}`, storedNoteTypeID(t, env, note.ID)))
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
