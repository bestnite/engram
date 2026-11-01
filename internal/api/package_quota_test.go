package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// The REST import entries (sharing ImportDeckPackage with MCP / CLI) must count newly added media in the package against
// **the importer's** quota. Before the fix REST import did not go through the upload chain's checkMediaQuota, so a package full of media could
// consume space without limit; hence this case is red before the fix (200) and green after (413 + media_quota_exceeded).

// apiPNGBody produces n bytes of valid media carrying the PNG magic number.
func apiPNGBody(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	return b
}

func apiMediaPackageZip(t *testing.T, body []byte) (raw []byte, sha string) {
	sum := sha256.Sum256(body)
	return fixturePackage(t, body, nil, true), hex.EncodeToString(sum[:])
}

// TestImportPackageCountsMediaAgainstImporterQuota asserts an over-quota import is rejected by the REST entries (413,
// stable code media_quota_exceeded), and no deck is persisted - body parsing and authorization have both passed,
// showing the rejection really happens at the quota checkpoint.
func TestImportPackageCountsMediaAgainstImporterQuota(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_quota_api", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_USER_QUOTA_BYTES", "")
	// The quota is 16 bytes < the package's 64-byte media (the key name matches media.SettingKeyMediaUserQuotaBytes).
	if err := store.PutSetting(context.Background(), env.db, "media_user_quota_bytes", "16", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	raw, _ := apiMediaPackageZip(t, apiPNGBody(64))
	body, ct := multipartBody(t, "deck.apkg", raw)
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota import status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	// The stable code uses the same literal as the upload chain (store.CodePackageQuotaExceeded == media_quota_exceeded).
	if resp.Error.Code != "media_quota_exceeded" {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, "media_quota_exceeded")
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Fatalf("rejected import created %d deck(s), want 0", decks)
	}
}

// TestImportPackageUsesTheImportersOwnQuota asserts the REST import charges media against the importer's own quota
// when an administrator set one: a personal limit refuses a package the instance quota would accept, and a personal
// 'unlimited' accepts a package the instance quota would refuse.
func TestImportPackageUsesTheImportersOwnQuota(t *testing.T) {
	cases := []struct {
		name     string
		instance string
		personal int64
		want     int
	}{
		{name: "personal limit below the package", instance: "0", personal: 16, want: http.StatusRequestEntityTooLarge},
		{name: "personal unlimited above the instance quota", instance: "16", personal: 0, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "pkg_own_quota", store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
			t.Setenv("MEDIA_USER_QUOTA_BYTES", "")
			if err := store.PutSetting(context.Background(), env.db, "media_user_quota_bytes", tc.instance, nil, time.Now().UTC()); err != nil {
				t.Fatalf("PutSetting: %v", err)
			}
			if err := store.NewUserStore(env.db).SetMediaQuota(context.Background(), user.ID, store.Ptr(tc.personal)); err != nil {
				t.Fatalf("SetMediaQuota: %v", err)
			}

			raw, _ := apiMediaPackageZip(t, apiPNGBody(64))
			body, ct := multipartBody(t, "deck.apkg", raw)
			rec := postImport(t, env, k.Plaintext, body, ct)
			if rec.Code != tc.want {
				t.Fatalf("import status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
