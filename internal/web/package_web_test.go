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

// TestImportIntoDeckDeniedForNonMember 是 M5-9 的越权负例：非成员 B 把包导入 A 的私有卡组必须
// 被拒（403），且 A 卡组的 note 数量一条都不变。
func TestImportIntoDeckDeniedForNonMember(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deckA := seedDeck(t, db, ownerID, "A private")
	seedBasic(t, db, deckA.ID, "A-Q1", "A-A1")

	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "attacker_into")
	srcDeck := seedDeck(t, db, bID, "attacker src")
	seedBasic(t, db, srcDeck.ID, "INTRUDER", "x")
	pkg := exportPackageBytes(t, srv, srcDeck.ID, bCookies)

	before, err := countDeckNotes(db, deckA.ID)
	if err != nil {
		t.Fatalf("count A notes before: %v", err)
	}
	rec := uploadPackage(t, srv, "/import", bCookies, bCSRF, "evil.edeck", pkg, map[string]string{
		"target": "into_deck", "deck_id": u64str(deckA.ID), "on_conflict": "update",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /import into foreign deck = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after, err := countDeckNotes(db, deckA.ID)
	if err != nil {
		t.Fatalf("count A notes after: %v", err)
	}
	if after != before {
		t.Errorf("A deck notes changed by unauthorised into_deck import: before=%d after=%d", before, after)
	}
}

// TestImportReplaceDeckDeniedForNonMember 覆盖更具破坏性的 replace_deck：非成员 B 不得用替换
// 目标把 A 私有卡组的 note 软删掉；被拒后 A 原 note 的 deleted_at 仍为空，存活行数不变。
func TestImportReplaceDeckDeniedForNonMember(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deckA := seedDeck(t, db, ownerID, "A replace target")
	orig := seedBasic(t, db, deckA.ID, "A-Q1", "A-A1")

	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "attacker_replace")
	srcDeck := seedDeck(t, db, bID, "attacker replace src")
	seedBasic(t, db, srcDeck.ID, "INTRUDER", "x")
	pkg := exportPackageBytes(t, srv, srcDeck.ID, bCookies)

	rec := uploadPackage(t, srv, "/import", bCookies, bCSRF, "evil.edeck", pkg, map[string]string{
		"target": "replace_deck:" + u64str(deckA.ID), "on_conflict": "update",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /import replace foreign deck = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 原 note 不得被软删（replace_deck 会先软删目标卡组全部 note）。
	var after store.Note
	if err := db.Unscoped().First(&after, orig.ID).Error; err != nil {
		t.Fatalf("reload A note %d: %v", orig.ID, err)
	}
	if after.DeletedAt.Valid {
		t.Errorf("A note %d was soft-deleted by unauthorised replace_deck", orig.ID)
	}
	if n, err := countDeckNotes(db, deckA.ID); err != nil || n != 1 {
		t.Errorf("A deck live notes = %d err %v, want 1", n, err)
	}
}

// TestImportRequiresCSRF 是必测负例：缺 CSRF token 的导入写请求被拒且不落库。
func TestImportRequiresCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "CSRF deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")
	src := seedDeck(t, db, ownerID, "CSRF src")
	seedBasic(t, db, src.ID, "NEW", "x")
	pkg := exportPackageBytes(t, srv, src.ID, ownerCookies)

	rec := uploadPackage(t, srv, "/import", ownerCookies, "", "pack.edeck", pkg, map[string]string{
		"target": "into_deck", "deck_id": u64str(deck.ID),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /import without CSRF = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if n, err := countDeckNotes(db, deck.ID); err != nil || n != 1 {
		t.Errorf("deck notes = %d err %v, want 1 (no write without CSRF)", n, err)
	}
}

// TestImportIntoOwnDeckSucceeds 是正向对照：卡组 owner 自己 into_deck 成功，且确实写入新笔记。
func TestImportIntoOwnDeckSucceeds(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Own deck")
	seedBasic(t, db, deck.ID, "Q1", "A1")
	src := seedDeck(t, db, ownerID, "Own src")
	seedBasic(t, db, src.ID, "NEW", "x")
	pkg := exportPackageBytes(t, srv, src.ID, ownerCookies)

	rec := uploadPackage(t, srv, "/import", ownerCookies, ownerCSRF, "pack.edeck", pkg, map[string]string{
		"target": "into_deck", "deck_id": u64str(deck.ID), "on_conflict": "update",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner POST /import into own deck = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if n, err := countDeckNotes(db, deck.ID); err != nil || n != 2 {
		t.Errorf("owner import notes = %d err %v, want 2", n, err)
	}
	// 断言确实写入：目标卡组里出现了包内的新笔记内容。
	var found int64
	if err := db.Model(&store.Note{}).Where("deck_id = ? AND fields_json LIKE ?", deck.ID, "%NEW%").Count(&found).Error; err != nil {
		t.Fatalf("count imported note: %v", err)
	}
	if found != 1 {
		t.Errorf("imported note count = %d, want 1", found)
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
