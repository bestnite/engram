package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminRoutesDenyNonAdmin is the core acceptance test: create an ordinary user and access
// every registered /admin/* page route one by one; all must be 403 (the guard runs before the shell).
func TestAdminRoutesDenyNonAdmin(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	_, cookies, _ := createUserAndLogin(t, srv, db, "plainuser")

	routes := adminRoutes()
	if len(routes) == 0 {
		t.Fatal("adminRoutes() is empty; the guard test would be vacuous")
	}
	for _, r := range routes {
		rec := getWithCookies(t, srv, r.Path, cookies)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-admin = %d, want 403 (body %s)", r.Method, r.Path, rec.Code, snippet(rec.Body.String()))
		}
	}

	// Anonymous access to the admin panel does not expose 403 semantics but goes back to the login page.
	anon := getWithCookies(t, srv, "/admin", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET /admin = %d, want 303 redirect to login", anon.Code)
	}
	// A privilege-escalation attempt writes an audit row.
	n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionPermissionDenied)
	if err != nil {
		t.Fatalf("count permission.denied audits: %v", err)
	}
	if n == 0 {
		t.Error("no permission.denied audit rows written for admin route attempts")
	}
}
