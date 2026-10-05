package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// F14：REST 导入入口的请求体上限验收。上限来源与 Web 上传一致（管理员配置的
// media_max_bytes），且必须在解析 multipart 之前生效，超限稳定 413。

// multipartBody 构造一个只含 file 字段的 multipart 请求体与其 Content-Type。
func multipartBody(t *testing.T, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// postImport 发一个带 bearer key 的 multipart 导入请求。
func postImport(t *testing.T, env *testEnv, key string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/import", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	env.router().ServeHTTP(rec, req)
	return rec
}

// minimalPackageZip 构造一个最小的合法 .edeck（一条 basic note，无媒体、无进度）。
func minimalPackageZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":1}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"q","back":"a"}}]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestImportPackageRejectsOversizedBody 是 F14 的核心负例：把 media_max_bytes 设为 256 字节，
// 一个 4 KiB 的 multipart 体必须在解析前被拒（413，code package_too_large）。
//
// 修前：handler 先 FormFile，gin 把 4 KiB 的 part 全读进内存后才处理，于是得到 400
// （不是 zip），而不是 413 —— 这正是本用例要红的原因。
func TestImportPackageRejectsOversizedBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_oversize", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	// 关掉环境变量覆盖，确保读的是 settings 表里的值。
	t.Setenv("MEDIA_MAX_BYTES", "")
	if err := env.db.Create(&store.Setting{Key: "media_max_bytes", Value: "256", UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("seed media_max_bytes: %v", err)
	}
	// TMPDIR 指向空目录：断言超限请求不留下 multipart 临时文件。
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	body, ct := multipartBody(t, "big.edeck", bytes.Repeat([]byte("A"), 4096))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if resp.Error.Code != store.CodePackageTooLarge {
		t.Errorf("error code = %q, want %q", resp.Error.Code, store.CodePackageTooLarge)
	}
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			t.Errorf("temp upload file left behind after rejection: %s", e.Name())
		}
	}
}

// TestImportPackageAcceptsBodyWithinLimit 是正向对照：默认上限内的合法包仍能导入成功。
func TestImportPackageAcceptsBodyWithinLimit(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_within", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_MAX_BYTES", "")

	body, ct := multipartBody(t, "deck.edeck", minimalPackageZip(t))
	rec := postImport(t, env, k.Plaintext, body, ct)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid import = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 1 {
		t.Errorf("decks = %d, want 1 after a successful import", decks)
	}
}
