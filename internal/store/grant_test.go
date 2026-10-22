package store

import (
	"context"
	"testing"
)

// TestGrantStoreLifecycle covers grant / change role / query / revoke on deck_grants.
func TestGrantStoreLifecycle(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "grant_owner", "grant_member")
			owner, member := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			d := Deck{OwnerUserID: owner, Name: "shared", PresetID: presetID}
			if err := NewDeckStore(db).Create(ctx, &d); err != nil {
				t.Fatalf("create deck: %v", err)
			}
			gs := NewGrantStore(db)

			// with no grant row it returns an empty string ('no grant' is not an error).
			if role, err := gs.Role(ctx, d.ID, member); err != nil || role != "" {
				t.Fatalf("Role(before grant) = (%q,%v), want (\"\",nil)", role, err)
			}

			// an invalid role is rejected and never stored.
			if err := gs.Grant(ctx, d.ID, member, "admin", nil); err == nil {
				t.Fatalf("Grant(invalid role) succeeded, want error")
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != "" {
				t.Fatalf("invalid role was persisted: %q", role)
			}

			// grant reader.
			if err := gs.Grant(ctx, d.ID, member, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			if role, err := gs.Role(ctx, d.ID, member); err != nil || role != RoleReader {
				t.Fatalf("Role(after grant) = (%q,%v), want (%q,nil)", role, err, RoleReader)
			}

			// a role change is an in-place update and does not produce a second row.
			if err := gs.Grant(ctx, d.ID, member, RoleEditor, Ptr(owner)); err != nil {
				t.Fatalf("change role: %v", err)
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != RoleEditor {
				t.Fatalf("Role(after change) = %q, want %q", role, RoleEditor)
			}
			rows, err := gs.ListByDeck(ctx, d.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("ListByDeck = (%d rows,%v), want (1,nil)", len(rows), err)
			}

			ids, err := gs.DeckIDsForUser(ctx, member)
			if err != nil || len(ids) != 1 || ids[0] != d.ID {
				t.Fatalf("DeckIDsForUser = (%v,%v), want [%d]", ids, err, d.ID)
			}

			// revoking deletes the whole row; the next query is immediately empty (revocation takes effect immediately).
			if err := gs.Revoke(ctx, d.ID, member); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if role, _ := gs.Role(ctx, d.ID, member); role != "" {
				t.Fatalf("Role(after revoke) = %q, want \"\"", role)
			}
			if rows, _ := gs.ListByDeck(ctx, d.ID); len(rows) != 0 {
				t.Fatalf("ListByDeck after revoke = %d rows, want 0", len(rows))
			}
		})
	}
}

// TestRolesForUser asserts that explicit grants aggregate by deck_id: granted decks appear with their role, ungranted decks do not,
// and a revoked grant disappears from the map. The list endpoint uses it to tell 'shared with me' from 'own / publicly visible only'.
func TestRolesForUser(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "roles_owner", "roles_member")
			owner, member := users[0], users[1]
			presetID := seedPresetRow(t, db, owner)
			gs := NewGrantStore(db)
			decks := NewDeckStore(db)

			mk := func(name string) *Deck {
				t.Helper()
				d := Deck{OwnerUserID: owner, Name: name, PresetID: presetID}
				if err := decks.Create(ctx, &d); err != nil {
					t.Fatalf("create deck %s: %v", name, err)
				}
				return &d
			}
			readerDeck := mk("roles-reader")
			editorDeck := mk("roles-editor")
			noneDeck := mk("roles-none")

			if err := gs.Grant(ctx, readerDeck.ID, member, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			if err := gs.Grant(ctx, editorDeck.ID, member, RoleEditor, Ptr(owner)); err != nil {
				t.Fatalf("grant editor: %v", err)
			}

			roles, err := gs.RolesForUser(ctx, member)
			if err != nil {
				t.Fatalf("RolesForUser() error = %v", err)
			}
			if len(roles) != 2 {
				t.Fatalf("RolesForUser() size = %d, want 2 (%v)", len(roles), roles)
			}
			if roles[readerDeck.ID] != RoleReader || roles[editorDeck.ID] != RoleEditor {
				t.Fatalf("RolesForUser() = %v, want %d=%q %d=%q", roles, readerDeck.ID, RoleReader, editorDeck.ID, RoleEditor)
			}
			if _, ok := roles[noneDeck.ID]; ok {
				t.Fatalf("RolesForUser() included un-granted deck %d", noneDeck.ID)
			}

			// after revocation that deck no longer appears in the map.
			if err := gs.Revoke(ctx, readerDeck.ID, member); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			roles, err = gs.RolesForUser(ctx, member)
			if err != nil {
				t.Fatalf("RolesForUser() after revoke error = %v", err)
			}
			if _, ok := roles[readerDeck.ID]; ok {
				t.Fatalf("RolesForUser() still lists revoked deck %d: %v", readerDeck.ID, roles)
			}
		})
	}
}

// TestRoleAllows asserts the permission table's 'at least' semantics: owner > editor > reader, and unknown roles never have rights.
func TestRoleAllows(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleEditor, true},
		{RoleOwner, RoleReader, true},
		{RoleEditor, RoleOwner, false},
		{RoleEditor, RoleEditor, true},
		{RoleEditor, RoleReader, true},
		{RoleReader, RoleOwner, false},
		{RoleReader, RoleEditor, false},
		{RoleReader, RoleReader, true},
		{"", RoleReader, false},
		{RoleOwner, "", false},
		{RoleOwner, "admin", false},
		{"root", RoleReader, false},
	}
	for _, c := range cases {
		if got := RoleAllows(c.have, c.want); got != c.ok {
			t.Errorf("RoleAllows(%q,%q) = %v, want %v", c.have, c.want, got, c.ok)
		}
	}
	for _, role := range []string{RoleOwner, RoleEditor, RoleReader} {
		if !ValidRole(role) {
			t.Errorf("ValidRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "admin", "root"} {
		if ValidRole(role) {
			t.Errorf("ValidRole(%q) = true, want false", role)
		}
	}
}
