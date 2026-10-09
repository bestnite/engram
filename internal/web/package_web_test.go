package web

import (
	"archive/zip"
	"bytes"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 卡组包浏览器路径仍留在 web 层的部分：导出下载。导入本身已切到 REST
// `POST /api/v1/decks/import`（SPA 的 client.ts 用它），因此 web 只保留导出与 /import 外壳。

// uploadPackage 以 multipart 拼一个 .edeck 上传请求；供仍按 REST 导入端点的测试复用。
func uploadPackage(t *testing.T, srv *Server, target string, cookies []*http.Cookie, csrf, filename string, body []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if csrf != "" {
		_ = w.WriteField("csrf_token", csrf)
	}
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestDeckPackageExportServesAttachment 断言导出端点的应答形态与鉴权：登录用户下载得到
// .edeck 附件（与 REST 导出同一 MIME），匿名访问被重定向登录页。
func TestDeckPackageExportServesAttachment(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Pack Deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	exp := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/package", cookies)
	if exp.Code != http.StatusOK {
		t.Fatalf("GET export = %d, want 200 (body %s)", exp.Code, snippet(exp.Body.String()))
	}
	if ct := exp.Header().Get("Content-Type"); ct != packageExportMediaType {
		t.Errorf("export content-type = %q, want %q", ct, packageExportMediaType)
	}
	if got := downloadName(t, exp); got != "Pack Deck.edeck" {
		t.Errorf("export filename = %q, want the deck title %q", got, "Pack Deck.edeck")
	}
	if exp.Body.Len() == 0 {
		t.Error("exported package is empty")
	}

	anon := getWithCookies(t, srv, "/decks/"+deck.PublicID+"/package", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous export = %d, want 303 redirect to login", anon.Code)
	}
}

// downloadName 从 Content-Disposition 解析出下载文件名（含 filename* 的 UTF-8 形式）。
func downloadName(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	cd := rec.Header().Get("Content-Disposition")
	disposition, params, err := mime.ParseMediaType(cd)
	if err != nil || disposition != "attachment" {
		t.Fatalf("Content-Disposition = %q, want an attachment (parse error %v)", cd, err)
	}
	return params["filename"]
}

// TestDeckPackageDownloadsAreNamedAfterTheDeck 断言 SPA 实际调用的 REST 导出用卡组标题命名，
// 中文标题经 filename* 编码后原样还原，而不是对外 id。
func TestDeckPackageDownloadsAreNamedAfterTheDeck(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "日语 N2 词汇")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	rec := getWithCookies(t, srv, "/api/v1/decks/"+deck.PublicID+"/package", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET REST export = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if got := downloadName(t, rec); got != "日语 N2 词汇.edeck" {
		t.Errorf("REST export filename = %q, want %q", got, "日语 N2 词汇.edeck")
	}
	if strings.Contains(rec.Header().Get("Content-Disposition"), deck.PublicID) {
		t.Errorf("Content-Disposition %q still carries the deck id", rec.Header().Get("Content-Disposition"))
	}
}

// TestBatchExportNamesEntriesAfterDecksWithoutCollisions 断言批量导出的 zip 条目以卡组标题命名，
// 同名卡组追加序号而不是互相覆盖。
func TestBatchExportNamesEntriesAfterDecksWithoutCollisions(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	first := seedDeck(t, db, ownerID, "Biology")
	second := seedDeck(t, db, ownerID, "Biology")
	third := seedDeck(t, db, ownerID, "a/b")

	rec := postJSONWithCSRF(t, srv, "/api/v1/decks/export-zip", map[string]any{
		"deck_ids": []string{first.PublicID, second.PublicID, third.PublicID},
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST export-zip = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read batch zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{"Biology.edeck", "Biology (2).edeck", "a_b.edeck"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("zip entries = %q, want %q", names, want)
	}
}

// TestImportPageServesShell 断言 /import 返回 SPA 外壳，路由鉴权由 API 会话端点负责。
func TestImportPageServesShell(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/import", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("anonymous GET /import = %d (%q), want SPA HTML shell", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// exportPackageBytes 通过浏览器导出端点取一个卡组包的字节，作为导入用例的合法载荷。
// 用真实导出的包（而不是手搓字节）保证负例失败的原因是判权，而不是坏包。
func exportPackageBytes(t *testing.T, srv *Server, deckPublicID string, cookies []*http.Cookie) []byte {
	t.Helper()
	rec := getWithCookies(t, srv, "/decks/"+deckPublicID+"/package", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("export deck %s = %d, want 200 (body %s)", deckPublicID, rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.Bytes()
	if len(body) == 0 {
		t.Fatalf("export deck %s produced an empty package", deckPublicID)
	}
	return body
}

// countDeckNotes 数一个卡组下的笔记条数，用于往返校验。
func countDeckNotes(db *gorm.DB, deckID uint64) (int64, error) {
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
