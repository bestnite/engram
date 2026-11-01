package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminUserDeleteRefusedSendsNothing pins the ordering of the side effects of a user deletion: the
// "account deleted" notice and the session revocation happen only after the admin gate has committed the
// deletion. The handler's pre-check is lock-free, so two admins deleting each other both pass it and the
// gate refuses the second with last_admin; that refusal must not mail the survivor or sign them out.
//
// The test therefore races two deletions, requires exactly one 204 and one 400 last_admin, and then checks
// that the refused target still receives no account-status mail and still holds an authenticated session.
func TestAdminUserDeleteRefusedSendsNothing(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	attachAdminMail(t, srv, db)
	ctx := context.Background()

	rec := adminPostJSON(t, srv, "/api/v1/admin/users", adminUserCreateRequest{
		Username: "other_admin", Email: "other_admin@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	}, ownerCookies, ownerCSRF)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second admin = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	other, err := store.NewUserStore(db).ByUsername(ctx, "other_admin")
	if err != nil || other == nil {
		t.Fatalf("second admin not found: %v", err)
	}
	otherCookies, otherCSRF := loginJSON(t, srv, db, "other_admin", "Sup3rSecret!")

	type outcome struct {
		code int
		body string
	}
	results := make(chan outcome, 2)
	go func() {
		r := adminPostJSON(t, srv, "/api/v1/admin/users/"+other.PublicID+"/delete", map[string]any{"confirm": true}, ownerCookies, ownerCSRF)
		results <- outcome{r.Code, r.Body.String()}
	}()
	go func() {
		r := adminPostJSON(t, srv, "/api/v1/admin/users/"+srvUserPublicID(t, db, ownerID)+"/delete", map[string]any{"confirm": true}, otherCookies, otherCSRF)
		results <- outcome{r.Code, r.Body.String()}
	}()

	succeeded, refused := 0, 0
	for i := 0; i < 2; i++ {
		got := <-results
		switch {
		case got.code == http.StatusNoContent:
			succeeded++
		case got.code == http.StatusBadRequest && strings.Contains(got.body, "last_admin"):
			refused++
		default:
			t.Fatalf("delete = %d %s, want 204 or 400 last_admin", got.code, snippet(got.body))
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("deletes: succeeded = %d, refused = %d, want 1 and 1", succeeded, refused)
	}

	// Exactly one account-status mail, addressed to the account that was really deleted.
	mails := outboxByType(t, db, mail.TypeAccountStatus)
	if len(mails) != 1 {
		t.Fatalf("account-status mails = %d, want 1: a refused deletion must not mail the surviving account", len(mails))
	}

	// The survivor keeps a usable session; only the deleted account's session was revoked.
	survivorCookies, deletedCookies := ownerCookies, otherCookies
	users := store.NewUserStore(db)
	if _, err := users.ByID(ctx, ownerID); err != nil {
		survivorCookies, deletedCookies = otherCookies, ownerCookies
	}
	if rec := getWithCookies(t, srv, "/api/v1/decks", survivorCookies); rec.Code != http.StatusOK {
		t.Fatalf("surviving admin GET /api/v1/decks = %d, want 200: a refused deletion revoked their session", rec.Code)
	}
	if rec := getWithCookies(t, srv, "/api/v1/decks", deletedCookies); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted admin GET /api/v1/decks = %d, want 401", rec.Code)
	}
}

// srvUserPublicID returns the public id of a user by internal id, for the delete route.
func srvUserPublicID(t *testing.T, db *gorm.DB, id uint64) string {
	t.Helper()
	u, err := store.NewUserStore(db).ByID(context.Background(), id)
	if err != nil || u == nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	return u.PublicID
}
