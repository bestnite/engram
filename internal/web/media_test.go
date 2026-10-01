package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// uploadMedia 以 multipart 提交一个名为 file 的上传；declared 作为该part的 Content-Type。
func uploadMedia(t *testing.T, srv *Server, cookie []*http.Cookie, csrf, filename, declared string, body []byte) *httptest.ResponseRecorder {
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

	req := httptest.NewRequest(http.MethodPost, "/media", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range cookie {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// textProto 是 multipart part 头的构造函数，避免依赖已弃用的 mime/multipart 辅助类型。
func textProto(filename, contentType string) map[string][]string {
	h := map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
	}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	return h
}

func pngBody() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("payload")...)
}

// TestMediaUploadDedupeAndServe 是 M2-8 的主验收：同文件两次上传只存一份，
// GET /media/:id 带 ETag 与 immutable 缓存，命中 ETag 返回 304。
func TestMediaUploadDedupeAndServe(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	body := pngBody()

	first := uploadMedia(t, srv, cookies, csrf, "a.png", "image/png", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}
	second := uploadMedia(t, srv, cookies, csrf, "b.png", "image/png", body)
	if second.Code != http.StatusCreated {
		t.Fatalf("second upload status = %d, want 201", second.Code)
	}
	var r1, r2 struct {
		ID     uint64 `json:"id"`
		Sha256 string `json:"sha256"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &r1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &r2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if r1.ID != r2.ID {
		t.Fatalf("dedupe failed: ids %d and %d differ", r1.ID, r2.ID)
	}
	var rows int64
	if err := db.Model(&store.Media{}).Count(&rows).Error; err != nil {
		t.Fatalf("count media: %v", err)
	}
	if rows != 1 {
		t.Errorf("media rows = %d, want 1", rows)
	}

	// 代理读取：ETag = sha256，Cache-Control immutable。
	get := getWithCookies(t, srv, "/media/"+strconv.FormatUint(r1.ID, 10), cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET /media/:id status = %d, want 200", get.Code)
	}
	etag := get.Header().Get("ETag")
	if etag != `"`+r1.Sha256+`"` {
		t.Errorf("ETag = %q, want quoted sha256 %q", etag, r1.Sha256)
	}
	if cc := get.Header().Get("Cache-Control"); cc == "" {
		t.Errorf("Cache-Control header missing")
	} else if !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if !bytes.Equal(get.Body.Bytes(), body) {
		t.Errorf("served bytes differ from uploaded bytes")
	}

	// 命中 ETag -> 304。
	req := httptest.NewRequest(http.MethodGet, "/media/"+strconv.FormatUint(r1.ID, 10), nil)
	req.Header.Set("If-None-Match", etag)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET status = %d, want 304", rec.Code)
	}
}

// TestMediaUploadRejectsOversized 覆盖管理员配置的上限生效：超过 media_max_bytes 被拒。
func TestMediaUploadRejectsOversized(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	if err := db.Create(&store.Setting{Key: "media_max_bytes", Value: "8", UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	rec := uploadMedia(t, srv, cookies, csrf, "big.png", "image/png", pngBody())
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code == "" {
		t.Errorf("error body has no stable code: %s", rec.Body.String())
	}
}

// TestMediaUploadRejectsMagicMismatch 覆盖魔数与声明不符被拒并返回稳定错误码。
func TestMediaUploadRejectsMagicMismatch(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	rec := uploadMedia(t, srv, cookies, csrf, "fake.png", "image/png", []byte("not an image at all"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("magic mismatch status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if env.Error.Code == "" {
		t.Errorf("error body has no stable code: %s", rec.Body.String())
	}
}

// TestMediaAnonymousAccessBlocked 覆盖匿名访问的边界：POST 被 CSRF 拦下（403），
// GET /media/:id 作为读取入口重定向到登录页。
func TestMediaAnonymousAccessBlocked(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := uploadMedia(t, srv, nil, "", "a.png", "image/png", pngBody())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous upload status = %d, want 403", rec.Code)
	}
	get := getWithCookies(t, srv, "/media/1", nil)
	if get.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET /media/1 status = %d, want 303", get.Code)
	}
	if loc := get.Header().Get("Location"); loc != "/login" {
		t.Fatalf("anonymous GET /media/1 Location = %q, want /login", loc)
	}
}
