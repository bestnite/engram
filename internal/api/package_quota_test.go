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

// REST 导入入口（与 MCP / CLI 共用 ImportDeckPackage）必须把包内新增媒体计入
// **导入者**配额。修前 REST 导入不经过上传链的 checkMediaQuota，一个装满媒体的包可以
// 无限占用空间；因此这条用例修前红（200），修后绿（413 + media_quota_exceeded）。

// apiPNGBody 造一段 n 字节、带 PNG 魔数的合法媒体字节。
func apiPNGBody(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	return b
}

// apiMediaPackageZip 构造一个最小合法 .edeck，含一条引用媒体条目的 note 与内联媒体字节。
func apiMediaPackageZip(t *testing.T, body []byte) (raw []byte, sha string) {
	t.Helper()
	sum := sha256.Sum256(body)
	sha = hex.EncodeToString(sum[:])
	mediaPath := "media/" + sha + ".png"
	files := map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":true,"include_reviews":false,"counts":{"notes":1,"cards":0,"media":1}}`,
		"notes.json":    fmt.Sprintf(`[{"kind":"basic","fields":{"front":"q","back":"%s"}}]`, mediaPath),
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
		"media.json":    fmt.Sprintf(`{"%s":{"path":"%s","mime":"image/png"}}`, sha, mediaPath),
		mediaPath:       string(body),
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

// TestImportPackageCountsMediaAgainstImporterQuota 断言超配额的导入被 REST 入口拒（413，
// 稳定 code media_quota_exceeded），且库里没有落任何卡组 —— 请求体解析与权限判定都已通过，
// 说明拒绝真的发生在配额检查点。
func TestImportPackageCountsMediaAgainstImporterQuota(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_quota_api", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_USER_QUOTA_BYTES", "")
	// 配额 16 字节 < 包内 64 字节媒体（键名与 media.SettingKeyMediaUserQuotaBytes 一致）。
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
	// 稳定 code 与上传链沿用同一个字面量（store.CodePackageQuotaExceeded == media_quota_exceeded）。
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
