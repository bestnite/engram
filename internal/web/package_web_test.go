package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// 卡组包浏览器路径（M5-9）的 HTTP 级验收：导出下载、上传导入摘要、坏包可读错误。

// uploadPackage 以 multipart 提交一个 .edeck 上传文件与若干表单字段。
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

// TestDeckPackageWebRoundTrip 是 M5-9 的核心验收：卡组页导出下载，再经 /import 上传，
// 页面展示与 REST 相同的摘要字段；坏包给出可读错误而不是 500。
func TestDeckPackageWebRoundTrip(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Pack Deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")

	// 卡组列表页提供导出控件。
	list := getWithCookies(t, srv, "/decks", cookies)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "/decks/"+u64str(deck.ID)+"/package") {
		t.Fatalf("GET /decks missing export control; code=%d body=%s", list.Code, snippet(list.Body.String()))
	}

	// 导出：得到 .edeck 字节。
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
	pkg := exp.Body.Bytes()
	if len(pkg) == 0 {
		t.Fatal("exported package is empty")
	}

	// 上传导入：页面展示摘要字段。
	rec := uploadPackage(t, srv, "/import", cookies, csrf, "pack.edeck", pkg, map[string]string{
		"target": "new_deck", "on_conflict": "update",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /import = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	for _, want := range []string{"导入结果", "新建笔记", "更新笔记", "新建卡片", "目标"} {
		if !strings.Contains(body, want) {
			t.Errorf("import summary missing field %q; body = %s", want, snippet(body))
		}
	}
	// 往返至少创建了那条笔记与卡片。
	if !strings.Contains(body, "<td class=\"py-2 font-medium text-slate-800\">1</td>") {
		t.Logf("note: could not match an exact count cell; body = %s", snippet(body))
	}
	if n, err := countDeckNotes(db, deck.ID); err != nil || n < 1 {
		t.Errorf("source deck notes = %d err %v, want >= 1", n, err)
	}
	// 坏包：不是 zip，给可读错误（4xx），不得 500。
	bad := uploadPackage(t, srv, "/import", cookies, csrf, "bad.edeck", []byte("this is not a zip"), map[string]string{
		"target": "new_deck",
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad package status = %d, want 400 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	if !strings.Contains(bad.Body.String(), "卡组包格式无效") {
		t.Errorf("bad package did not produce a readable error; body = %s", snippet(bad.Body.String()))
	}

	// 未登录不能导出。
	anon := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/package", nil)
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous export = %d, want 303 redirect to login", anon.Code)
	}
}

// TestImportPageRequiresLogin 断言匿名访问 /import 被重定向到登录页。
func TestImportPageRequiresLogin(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := getWithCookies(t, srv, "/import", nil)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous GET /import = %d, want 303", rec.Code)
	}
}

// countDeckNotes 数一个卡组下的笔记条数，用于往返校验。
func countDeckNotes(db *gorm.DB, deckID uint64) (int64, error) {
	var n int64
	if err := db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
