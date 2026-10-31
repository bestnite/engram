package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminUserMediaQuota is the acceptance of the per-user media quota: an administrator sets a limit, an explicit
// unlimited or 'follow the instance' for one user, every upload of that user is checked against it, and other users
// keep the instance-wide quota.
func TestAdminUserMediaQuota(t *testing.T) {
	srv, db, _, adminCookies, adminCSRF := newNotesServer(t)
	ctx := context.Background()
	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	userID, userCookies, userCSRF := createUserAndLogin(t, srv, db, "quota_user")
	target, err := store.NewUserStore(db).ByID(ctx, userID)
	if err != nil {
		t.Fatalf("load quota user: %v", err)
	}
	setQuota := func(body map[string]any) int {
		t.Helper()
		return adminPostJSON(t, srv, "/api/v1/admin/users/"+target.PublicID+"/media-quota", body, adminCookies, adminCSRF).Code
	}
	upload := func(cookies []*http.Cookie, csrf string, size int) int {
		t.Helper()
		return uploadMedia(t, srv, cookies, csrf, "q.png", "image/png", quotaPNG(size)).Code
	}

	t.Run("a limit applies to that user only", func(t *testing.T) {
		if code := setQuota(map[string]any{"mode": "limit", "bytes": 1000}); code != http.StatusNoContent {
			t.Fatalf("set limit = %d, want 204", code)
		}
		if code := upload(userCookies, userCSRF, 1500); code != http.StatusRequestEntityTooLarge {
			t.Errorf("user upload over own limit = %d, want 413", code)
		}
		// The administrator still has the instance-wide 2048 bytes.
		if code := upload(adminCookies, adminCSRF, 1500); code != http.StatusCreated {
			t.Errorf("other user upload under instance quota = %d, want 201", code)
		}
	})

	t.Run("unlimited lifts the instance quota, even one set by the environment", func(t *testing.T) {
		t.Setenv(envMediaUserQuotaBytes, "2048")
		if code := setQuota(map[string]any{"mode": "unlimited"}); code != http.StatusNoContent {
			t.Fatalf("set unlimited = %d, want 204", code)
		}
		if code := upload(userCookies, userCSRF, 6000); code != http.StatusCreated {
			t.Errorf("unlimited user upload = %d, want 201", code)
		}
	})

	t.Run("default returns the user to the instance quota", func(t *testing.T) {
		if code := setQuota(map[string]any{"mode": "default"}); code != http.StatusNoContent {
			t.Fatalf("set default = %d, want 204", code)
		}
		if code := upload(userCookies, userCSRF, 3000); code != http.StatusRequestEntityTooLarge {
			t.Errorf("upload over instance quota after reset = %d, want 413", code)
		}
		fresh, err := store.NewUserStore(db).ByID(ctx, userID)
		if err != nil {
			t.Fatalf("reload user: %v", err)
		}
		if fresh.MediaQuotaBytes != nil {
			t.Errorf("stored quota after default = %d, want NULL", *fresh.MediaQuotaBytes)
		}
	})

	t.Run("the list reports the setting, the usage and the instance default", func(t *testing.T) {
		if code := setQuota(map[string]any{"mode": "limit", "bytes": 5 << 20}); code != http.StatusNoContent {
			t.Fatalf("set limit = %d, want 204", code)
		}
		rec := getJSON(t, srv, "/api/v1/admin/users", adminCookies, nil)
		var list adminUsersResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode users: %v", err)
		}
		if list.DefaultMediaQuotaBytes != 2048 {
			t.Errorf("default_media_quota_bytes = %d, want 2048", list.DefaultMediaQuotaBytes)
		}
		var found bool
		for _, row := range list.Users {
			switch row.ID {
			case target.PublicID:
				found = true
				if row.MediaQuotaBytes == nil || *row.MediaQuotaBytes != 5<<20 {
					t.Errorf("listed quota = %v, want %d", row.MediaQuotaBytes, 5<<20)
				}
				// The unlimited upload is still pending (no note references it), so it is charged to the uploader.
				if row.MediaUsedBytes != 6000 {
					t.Errorf("listed usage = %d, want 6000", row.MediaUsedBytes)
				}
			default:
				if row.MediaQuotaBytes != nil {
					t.Errorf("user %s quota = %d, want null", row.Username, *row.MediaQuotaBytes)
				}
			}
		}
		if !found {
			t.Fatalf("quota user missing from the list")
		}
	})

	t.Run("every change is audited", func(t *testing.T) {
		var n int64
		db.Model(&store.AuditLog{}).Where("action = ? AND target_id = ?", store.ActionUserMediaQuota, userID).Count(&n)
		if n != 4 {
			t.Errorf("audit rows = %d, want 4", n)
		}
	})

	t.Run("refusals", func(t *testing.T) {
		cases := []struct {
			name string
			body map[string]any
			want int
		}{
			{"limit without bytes", map[string]any{"mode": "limit"}, http.StatusBadRequest},
			{"negative limit", map[string]any{"mode": "limit", "bytes": -5}, http.StatusBadRequest},
			{"unknown mode", map[string]any{"mode": "half"}, http.StatusBadRequest},
		}
		for _, tc := range cases {
			if code := setQuota(tc.body); code != tc.want {
				t.Errorf("%s = %d, want %d", tc.name, code, tc.want)
			}
		}
		if code := adminPostJSON(t, srv, "/api/v1/admin/users/no-such-user/media-quota", map[string]any{"mode": "default"}, adminCookies, adminCSRF).Code; code != http.StatusNotFound {
			t.Errorf("unknown user = %d, want 404", code)
		}
		if code := adminPostJSON(t, srv, "/api/v1/admin/users/"+target.PublicID+"/media-quota", map[string]any{"mode": "unlimited"}, userCookies, userCSRF).Code; code != http.StatusForbidden {
			t.Errorf("non-admin = %d, want 403", code)
		}
		if code := adminPostJSON(t, srv, "/api/v1/admin/users/"+target.PublicID+"/media-quota", map[string]any{"mode": "unlimited"}, adminCookies, "").Code; code != http.StatusForbidden {
			t.Errorf("missing CSRF = %d, want 403", code)
		}
		// None of the refused requests changed the stored limit.
		fresh, err := store.NewUserStore(db).ByID(ctx, userID)
		if err != nil {
			t.Fatalf("reload user: %v", err)
		}
		if fresh.MediaQuotaBytes == nil || *fresh.MediaQuotaBytes != 5<<20 {
			t.Errorf("stored quota after refusals = %v, want %d", fresh.MediaQuotaBytes, 5<<20)
		}
	})
}
