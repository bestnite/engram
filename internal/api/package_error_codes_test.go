package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是“卡组包错误码逐码浮出”的 REST 端验收（DESIGN.md §7.3、§7.6）：
// mapPackageError 不再把 package_bad_format 折叠成笼统的 invalid_request，而是逐码透出
// store.PackageError.Code。每条用例同时断言 HTTP 状态与修前一致（本任务只改 body 里的 code）。
//
// 修前：坏 zip 会得到 code=invalid_request（用例红）；修后得到 code=package_bad_format（绿）。

// decodePackageErrorCode 解出错误包壳里的稳定 code 与 message。
func decodePackageErrorCode(t *testing.T, raw []byte) (code, message string) {
	t.Helper()
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, raw)
	}
	return resp.Error.Code, resp.Error.Message
}

// packageZip 用给定的条目名→内容构造一个 zip 卡组包字节。
func packageZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
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

// presetJSON 是最小合法 preset.json（与 minimalPackageZip 同）。
const presetJSON = `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`

// TestImportPackageSurfacesBadFormat 断言坏 zip（不是归档）产出它自己的稳定 code
// package_bad_format，状态仍是 400，且 message 不再是笼统的“请求不合法”。
func TestImportPackageSurfacesBadFormat(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_bad_format", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	body, ct := multipartBody(t, "bad.edeck", []byte("this is not a zip"))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad-format import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageBadFormat {
		t.Errorf("bad-format import code = %q, want %q", code, store.CodePackageBadFormat)
	}
	// 逐码透出后，message 是语言包 error.package_bad_format 的英文文案，不再落到 invalid_request。
	if msg == errorMessages[CodeInvalidRequest] {
		t.Errorf("bad-format import message fell back to invalid_request: %q", msg)
	}
}

// TestImportPackageSurfacesTooLarge 断言超出请求体上限的包产出稳定 code package_too_large，
// 状态与修前一致（413）。
func TestImportPackageSurfacesTooLarge(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_too_large", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv("MEDIA_MAX_BYTES", "")
	if err := store.PutSetting(context.Background(), env.db, "media_max_bytes", "256", nil, time.Now().UTC()); err != nil {
		t.Fatalf("seed media_max_bytes: %v", err)
	}

	body, ct := multipartBody(t, "big.edeck", bytes.Repeat([]byte("A"), 4096))
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	code, _ := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageTooLarge {
		t.Errorf("oversized import code = %q, want %q", code, store.CodePackageTooLarge)
	}
}

// TestImportPackageSurfacesUnknownKind 断言包内未知题型产出稳定 code package_unknown_kind，
// 状态仍是 400，且逐条列出出错的 note 下标（entries 不丢失）。
func TestImportPackageSurfacesUnknownKind(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "pkg_unknown_kind", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	raw := packageZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"imported"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":1,"cards":0}}`,
		"notes.json":    `[{"kind":"no_such_kind","fields":{"front":"q","back":"a"}}]`,
		"cards.json":    `[]`,
		"preset.json":   presetJSON,
	})
	body, ct := multipartBody(t, "unknown.edeck", raw)
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-kind import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageUnknownKind {
		t.Errorf("unknown-kind import code = %q, want %q", code, store.CodePackageUnknownKind)
	}
	// entries 仍逐条列出：message 里点名出错的 note 下标。
	if !bytes.Contains([]byte(msg), []byte("notes[0]")) {
		t.Errorf("unknown-kind message lost the per-note entries: %q", msg)
	}
}

// TestImportPackageSurfacesUnsafeMedia 断言包内媒体真实字节不在白名单内时产出稳定 code
// package_unsafe_media，状态仍是 400（媒体以真实字节判定，声明与字节不符即整包被拒）。
func TestImportPackageSurfacesUnsafeMedia(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	// 校验发生在写盘之前；给一个临时媒体根目录，确保拒绝不是因为没有落盘位置。
	env.api.mediaRoot = t.TempDir()
	user := seedUser(t, env.db, "pkg_unsafe_media", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)

	// 声明 image/png，真实字节是 HTML：真实类型判定与声明不符 → 拒绝。
	raw, _ := apiMediaPackageZip(t, []byte("<html><body><script>alert(1)</script></body></html>"))
	body, ct := multipartBody(t, "unsafe.edeck", raw)
	rec := postImport(t, env, k.Plaintext, body, ct)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe-media import status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	code, _ := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != store.CodePackageUnsafeMedia {
		t.Errorf("unsafe-media import code = %q, want %q", code, store.CodePackageUnsafeMedia)
	}
}
