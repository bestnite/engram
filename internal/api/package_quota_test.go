package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// apiMediaPackageZip builds a minimal valid .edeck containing one note that references a media entry, plus the inline media bytes.
func apiMediaPackageZip(t *testing.T, body []byte) (raw []byte, sha string) {
	t.Helper()
	sum := sha256.Sum256(body)
	sha = hex.EncodeToString(sum[:])
	mediaPath := "media/" + sha + ".png"
	files := map[string]string{
		"manifest.json":   `{"format_version":2,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`,
		"note_types.json": packageTestNoteTypesJSON,
		"notes.json":      fmt.Sprintf(`[{"note_type":"nt-1","fields":{"front":"q","back":"%s"}}]`, mediaPath),
		"cards.json":      `[]`,
		"preset.json":     `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
		"media.json":      fmt.Sprintf(`{"%s":{"path":"%s","mime":"image/png"}}`, sha, mediaPath),
		mediaPath:         string(body),
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes(), sha
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
	body, ct := multipartBody(t, "deck.edeck", raw)
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
