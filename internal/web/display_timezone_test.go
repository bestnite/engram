package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the times the server formats for display: the admin pages show them in the viewing admin's
// timezone, a mail shows them in the recipient's, and the session hands the account's timezone to the client, which
// formats every other time itself. An empty or unknown timezone reads as UTC everywhere.

// TestAdminPagesFormatTimesInAdminTimezone: the registration, OIDC and mail template pages follow the admin's
// timezone, like the audit and API key pages.
func TestAdminPagesFormatTimesInAdminTimezone(t *testing.T) {
	at := time.Date(2026, 1, 2, 20, 30, 0, 0, time.UTC)
	cases := []struct {
		tz   string
		want string
	}{
		{"UTC", "2026-01-02 20:30"},
		// The local day is the day after the UTC one.
		{"Asia/Shanghai", "2026-01-03 04:30"},
		{"America/New_York", "2026-01-02 15:30"},
		{"", "2026-01-02 20:30"},
		{"Mars/Phobos", "2026-01-02 20:30"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			srv, db, ownerID, cookies, _ := newNotesServer(t)
			srv.invites = store.NewInviteStore(db)
			if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("timezone", tc.tz).Error; err != nil {
				t.Fatalf("set timezone: %v", err)
			}
			seeds := []any{
				&store.Invite{Token: "tz-invite", Role: "user", CreatedAt: at, ExpiresAt: &at, UsedAt: &at},
				&store.Identity{UserID: ownerID, Provider: "https://issuer.example.com", Subject: "tz-subject", LinkedAt: at},
				&store.MailTemplate{Type: "password_reset", Locale: "en", Subject: "s", BodyMD: "b", UpdatedAt: at},
			}
			for _, row := range seeds {
				if err := db.Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}

			var reg adminRegistrationResponse
			decodeOK(t, getJSON(t, srv, "/api/v1/admin/registration", cookies, nil), &reg)
			if len(reg.Invites) != 1 {
				t.Fatalf("invites = %+v, want the seeded one", reg.Invites)
			}
			if inv := reg.Invites[0]; inv.CreatedAt != tc.want || inv.ExpiresAt != tc.want || inv.UsedAt != tc.want {
				t.Errorf("invite times = %q/%q/%q, want %q", inv.CreatedAt, inv.ExpiresAt, inv.UsedAt, tc.want)
			}

			var oidc adminOIDCResponse
			decodeOK(t, getJSON(t, srv, "/api/v1/admin/oidc", cookies, nil), &oidc)
			if len(oidc.Identities) != 1 || oidc.Identities[0].LinkedAt != tc.want {
				t.Errorf("identities = %+v, want linked_at %q", oidc.Identities, tc.want)
			}

			var tpl mailTemplatesResponse
			decodeOK(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil), &tpl)
			if len(tpl.Rows) != 1 || tpl.Rows[0].UpdatedAt != tc.want {
				t.Errorf("mail template rows = %+v, want updated_at %q", tpl.Rows, tc.want)
			}
		})
	}
}

// TestSessionCarriesTimezone: the session probe hands the client the stored timezone, which it formats times in.
func TestSessionCarriesTimezone(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	if err := db.Model(&store.User{}).Where("id = ?", ownerID).Update("timezone", "Asia/Shanghai").Error; err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	var got struct {
		User struct {
			Timezone string `json:"timezone"`
		} `json:"user"`
	}
	decodeOK(t, getWithCookies(t, srv, "/api/v1/auth/session", cookies), &got)
	if got.User.Timezone != "Asia/Shanghai" {
		t.Fatalf("session timezone = %q, want Asia/Shanghai", got.User.Timezone)
	}
}

// TestMailTime: a mail names the clock it uses; a recipient who is no user, or whose timezone is empty or unknown,
// reads UTC.
func TestMailTime(t *testing.T) {
	at := time.Date(2026, 1, 2, 20, 30, 0, 0, time.UTC)
	cases := []struct {
		name      string
		recipient *store.User
		want      string
	}{
		{"no user", nil, "2026-01-02 20:30 UTC"},
		{"utc", &store.User{Timezone: "UTC"}, "2026-01-02 20:30 UTC"},
		{"shanghai", &store.User{Timezone: "Asia/Shanghai"}, "2026-01-03 04:30 Asia/Shanghai"},
		{"empty", &store.User{Timezone: ""}, "2026-01-02 20:30 UTC"},
		{"unknown", &store.User{Timezone: "Mars/Phobos"}, "2026-01-02 20:30 UTC"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mailTime(at, tc.recipient); got != tc.want {
				t.Fatalf("mailTime = %q, want %q", got, tc.want)
			}
		})
	}
}

// decodeOK asserts a 200 response and decodes its JSON body into out.
func decodeOK(t *testing.T, rec interface {
	Result() *http.Response
}, out any) {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
