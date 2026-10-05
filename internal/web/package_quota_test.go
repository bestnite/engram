package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// F15：浏览器导入入口（POST /import）必须与 REST/MCP/CLI 一样，把包内新增媒体计入
// 导入者配额。修前 web 导入不经过上传链的 checkMediaQuota，超配额的包照常写入；
// 因此这条用例修前红（200），修后绿（413 且库中无卡组）。

// webMediaPackageZip 构造一个最小合法 .edeck，含一条引用媒体条目的 note 与内联媒体字节。
func webMediaPackageZip(t *testing.T, body []byte) (raw []byte, sha string) {
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

// postImportWeb 以 multipart 提交一个 .edeck 到 POST /import（带会话 cookie 与 CSRF 字段）。
func postImportWeb(t *testing.T, srv *Server, cookies []*http.Cookie, csrf, filename string, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("csrf_token", csrf)
	_ = w.WriteField("target", store.PackageTargetNewDeck)
	part, err := w.CreatePart(textProto(filename, "application/vnd.engram.edeck"))
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(raw); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestImportSubmitCountsMediaAgainstImporterQuota 断言超配额的浏览器导入被拒：413，渲染本地化
// 文案（不泄漏语言包键名），且库中不留任何卡组 —— 证明拒绝发生在写入之前。
func TestImportSubmitCountsMediaAgainstImporterQuota(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(context.Background(), db, settingKeyMediaUserQuotaBytes, "16", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	raw, _ := webMediaPackageZip(t, quotaPNG(64))
	rec := postImportWeb(t, srv, cookies, csrf, "deck.edeck", raw)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota import status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 文案必须来自语言包；缺键时 Localizer 会回退成键名本身，这里据此抓缺键。
	if strings.Contains(body, "import.error.") {
		t.Fatalf("over-quota message leaked a raw catalog key:\n%s", body)
	}
	var decks int64
	if err := db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Fatalf("rejected import created %d deck(s), want 0", decks)
	}
}
