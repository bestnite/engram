package web

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖：编辑器里的媒体面——上传图片并插入到卡片字段。
// 验收主线是「上传的图片在预览里渲染，且刷新页面后仍在」；同时给出权限与校验的反面用例。

// uploadMediaTo 向指定上传端点提交 multipart，用于区分 /media 与 /decks/:id/media 两个入口。
func uploadMediaTo(t *testing.T, srv *Server, cookie []*http.Cookie, target, csrf, filename, declared string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if csrf != "" {
		_ = w.WriteField("csrf_token", csrf)
	}
	part, err := w.CreatePart(textProto(filename, declared))
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range cookie {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// mediaUploadResp 是上传成功返回的元数据。
type mediaUploadResp struct {
	Sha256 string `json:"sha256"`
	Mime   string `json:"mime"`
	URL    string `json:"url"`
}

// mediaShaOK 校验上传/URL 里的 sha256 形状：恰好 64 位小写十六进制。
func mediaShaOK(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// TestEditorMediaUploadInsertsAndSurvivesReload 是主验收：从编辑器使用的卡组上传
// 入口上传图片 → 以 Markdown 引用插入字段 → 预览渲染出 <img> → 保存 → 刷新后字段值里仍
// 有该引用、预览里仍有 <img>（且不是 base64 内联）。
func TestEditorMediaUploadInsertsAndSurvivesReload(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Media deck")
	note := seedBasic(t, db, deck.ID, "front", "back")
	deckPath := "/decks/" + u64str(deck.ID)

	// 1) 从卡组内上传入口上传图片。
	up := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrf, "pic.png", "image/png", pngBody())
	if up.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) || saved.URL != "/media/"+saved.Sha256 {
		t.Fatalf("upload returned url %q sha %q, want /media/<sha256>", saved.URL, saved.Sha256)
	}

	// 2) 代理能取回该图片（走已有的 /media/<sha256>，而非另写一套存储）。
	img := getWithCookies(t, srv, saved.URL, cookies)
	if img.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, img.Code)
	}
	if ct := img.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("GET %s Content-Type = %q, want image/png", saved.URL, ct)
	}

	// 3) 把 Markdown 图片引用插入 front 字段，SPA 预览端点应渲染出 <img>。
	ref := "![](" + saved.URL + ")"
	prev := postJSONWithCSRF(t, srv, "/api/v1/decks/"+u64str(deck.ID)+"/notes/preview", map[string]any{
		"kind":   "basic",
		"fields": map[string]any{"front": "see " + ref, "back": "back"},
	}, cookies, csrf)
	if prev.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (body %s)", prev.Code, prev.Body.String())
	}
	var prevBody struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(prev.Body.Bytes(), &prevBody); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(prevBody.Cards) == 0 || !strings.Contains(prevBody.Cards[0].FrontHTML, `<img src="`+saved.URL+`"`) {
		t.Fatalf("preview did not render the uploaded image: %s", snippet(prev.Body.String()))
	}

	// 4) 保存该引用（SPA 的卡片更新走 PATCH /api/v1/notes/:id）。
	save := jsonRequest(t, srv, http.MethodPatch, "/api/v1/notes/"+u64str(note.ID),
		`{"kind":"basic","fields":{"front":"see `+ref+`","back":"back"}}`, cookies, csrf)
	if save.Code != http.StatusOK {
		t.Fatalf("save status = %d, want 200 (body %s)", save.Code, save.Body.String())
	}

	// 5) 再次预览：字段值里仍有引用，预览里仍有 <img>（且不是 base64 内联）。
	reload := postJSONWithCSRF(t, srv, "/api/v1/decks/"+u64str(deck.ID)+"/notes/preview", map[string]any{
		"kind":   "basic",
		"fields": map[string]any{"front": "see " + ref, "back": "back"},
	}, cookies, csrf)
	if reload.Code != http.StatusOK {
		t.Fatalf("reload preview status = %d, want 200", reload.Code)
	}
	var reloadBody struct {
		Cards []struct {
			FrontHTML string `json:"front_html"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(reload.Body.Bytes(), &reloadBody); err != nil {
		t.Fatalf("decode reload preview: %v", err)
	}
	if len(reloadBody.Cards) == 0 {
		t.Fatalf("reload preview returned no cards: %s", snippet(reload.Body.String()))
	}
	body := reloadBody.Cards[0].FrontHTML
	if !strings.Contains(body, saved.URL) {
		t.Errorf("reloaded preview lost the /media reference: %s", snippet(body))
	}
	if !strings.Contains(body, `<img src="`+saved.URL+`"`) {
		t.Errorf("reloaded preview lost the <img>: %s", snippet(body))
	}
	if strings.Contains(body, "data:image") {
		t.Errorf("media was inlined as base64 instead of referenced")
	}

	// 存储层确认字段里存的是引用而非内联数据。
	stored, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("load note: %v", err)
	}
	fields, err := store.ParseFields(stored.FieldsJSON)
	if err != nil {
		t.Fatalf("parse fields: %v", err)
	}
	if front, _ := fields["front"].(string); !strings.Contains(front, saved.URL) {
		t.Errorf("stored front %q does not contain the reference %q", front, saved.URL)
	}
}

// TestEditorMediaUploadRejectsOversizedAndWrongMagic 覆盖上传校验的反面用例：
// 超过管理员配置的上限 -> 413 + media_too_large；声明的类型与魔数不符 -> 422 + media_magic_mismatch。
func TestEditorMediaUploadRejectsOversizedAndWrongMagic(t *testing.T) {
	t.Setenv("MEDIA_MAX_BYTES", "1024")
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Limits deck")
	deckPath := "/decks/" + u64str(deck.ID)

	// 超限：合法 PNG 头 + 填充，超过 1 KiB。
	big := append(pngBody(), bytes.Repeat([]byte("x"), 2048)...)
	over := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrf, "big.png", "image/png", big)
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413 (body %s)", over.Code, over.Body.String())
	}
	if !strings.Contains(over.Body.String(), "media_too_large") {
		t.Errorf("oversized upload did not return the stable code: %s", over.Body.String())
	}

	// 错魔数：声明 png 但内容是文本。
	bad := uploadMediaTo(t, srv, cookies, deckPath+"/media", csrfOrEmpty(csrf), "fake.png", "image/png", []byte("this is not a png"))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong-magic upload status = %d, want 422 (body %s)", bad.Code, bad.Body.String())
	}
	if !strings.Contains(bad.Body.String(), "media_magic_mismatch") {
		t.Errorf("wrong-magic upload did not return the stable code: %s", bad.Body.String())
	}
}

// TestEditorMediaUploadRequiresEditorRoleAndCSRF 覆盖权限与 CSRF 的反面用例：
// 读者不能往别人的卡组塞媒体；缺少 CSRF 的上传被拒。
func TestEditorMediaUploadRequiresEditorRoleAndCSRF(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Role deck")
	deckPath := "/decks/" + u64str(deck.ID)

	// 授予第二个用户 reader 角色。
	user2ID, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "media-reader")
	grantRole(t, srv, deck.ID, user2ID, store.RoleReader, ownerCookies, ownerCSRF)

	// 读者上传被拒 403。
	reader := uploadMediaTo(t, srv, u2Cookies, deckPath+"/media", u2CSRF, "pic.png", "image/png", pngBody())
	if reader.Code != http.StatusForbidden {
		t.Fatalf("reader upload status = %d, want 403 (body %s)", reader.Code, reader.Body.String())
	}

	// 无 CSRF 的上传被拒 403。
	noCSRF := uploadMediaTo(t, srv, ownerCookies, deckPath+"/media", "", "pic.png", "image/png", pngBody())
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("upload without CSRF status = %d, want 403 (body %s)", noCSRF.Code, noCSRF.Body.String())
	}
}

// csrfOrEmpty 让调用点读起来更直白（错误魔数用例仍带合法 CSRF，只有 CSRF 用例才留空）。
func csrfOrEmpty(s string) string { return s }
