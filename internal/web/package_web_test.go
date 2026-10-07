package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 卡组包浏览器路径（M5-9）仍留在 web 层的部分：导出下载。导入本身已切到 REST
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

	exp := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/package", cookies)
	if exp.Code != http.StatusOK {
		t.Fatalf("GET export = %d, want 200 (body %s)", exp.Code, snippet(exp.Body.String()))
	}
	if ct := exp.Header().Get("Content-Type"); ct != packageExportMediaType {
		t.Errorf("export content-type = %q, want %q", ct, packageExportMediaType)
	}
	if cd := exp.Header().Get("Content-Disposition"); !strings.Contains(cd, ".edeck") {
		t.Errorf("export content-disposition = %q, want a .edeck download", cd)
	}
	if exp.Body.Len() == 0 {
		t.Error("exported package is empty")
	}

	anon := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/package", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous export = %d, want 303 redirect to login", anon.Code)
	}
}

// TestImportPageServesSPAShell 断言 /import 返回 SPA 外壳，路由鉴权由 API 会话端点负责。
func TestImportPageServesSPAShell(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/import", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("anonymous GET /import = %d (%q), want SPA HTML shell", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// exportPackageBytes 通过浏览器导出端点取一个卡组包的字节，作为导入用例的合法载荷。
// 用真实导出的包（而不是手搓字节）保证负例失败的原因是判权，而不是坏包。
func exportPackageBytes(t *testing.T, srv *Server, deckID uint64, cookies []*http.Cookie) []byte {
	t.Helper()
	rec := getWithCookies(t, srv, "/decks/"+u64str(deckID)+"/package", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("export deck %d = %d, want 200 (body %s)", deckID, rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.Bytes()
	if len(body) == 0 {
		t.Fatalf("export deck %d produced an empty package", deckID)
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
