package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/store"
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
// GET /media/<sha256> 带 ETag 与 immutable 缓存，命中 ETag 返回 304。
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
		Sha256 string `json:"sha256"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &r1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &r2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if !mediaShaOK(r1.Sha256) {
		t.Fatalf("first upload sha = %q, want a 64-hex sha256", r1.Sha256)
	}
	if r1.Sha256 != r2.Sha256 {
		t.Fatalf("dedupe failed: shas %s and %s differ", r1.Sha256, r2.Sha256)
	}
	var rows int64
	if err := db.Model(&store.Media{}).Count(&rows).Error; err != nil {
		t.Fatalf("count media: %v", err)
	}
	if rows != 1 {
		t.Errorf("media rows = %d, want 1", rows)
	}

	// 代理读取：ETag = sha256，Cache-Control immutable。
	get := getWithCookies(t, srv, "/media/"+r1.Sha256, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET /media/<sha256> status = %d, want 200", get.Code)
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
	req := httptest.NewRequest(http.MethodGet, "/media/"+r1.Sha256, nil)
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

// dbQuerySpy 记录 GORM 发出的 SQL，用于证明「非法 sha 的请求没有查库」。
type dbQuerySpy struct {
	mu    sync.Mutex
	lines []string
}

func (s *dbQuerySpy) Printf(msg string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// logger.Writer 收到的是格式串 + 参数，必须自己格式化，否则存下来的是 "%s"。
	s.lines = append(s.lines, fmt.Sprintf(msg, args...))
}

func (s *dbQuerySpy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = nil
}

func (s *dbQuerySpy) touchedMedia() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.lines {
		// 只认反引号包住的表名；日志里的源文件路径（.../media-l1/...）也含 "media"，
		// 用裸 "media" 匹配会把「没查库」误判成「查了库」。
		if strings.Contains(l, "`media`") {
			return true
		}
	}
	return false
}

// TestMediaServeRejectsNonShaWithoutDBQuery 覆盖形状校验：短于/长于 64 位、含非十六进制
// 字符、旧的数字 id、带路径穿越字符的参数一律 404，且在 404 之前不接触 media 表。
func TestMediaServeRejectsNonShaWithoutDBQuery(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	// 先放一行真实媒体：如果处理器会拿非法参数去查库，这里就有可能命中而 200。
	rec := uploadMedia(t, srv, cookies, csrf, "real.png", "image/png", pngBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	spy := &dbQuerySpy{}
	db.Logger = logger.New(spy, logger.Config{LogLevel: logger.Info})

	// 正向对照：一次合法的媒体读取必须被 spy 看到。否则下面的「没有 media SQL」是假的
	// （spy 根本没接上，而不是处理器没查库）。
	spy.reset()
	if got := getWithCookies(t, srv, saved.URL, cookies); got.Code != http.StatusOK {
		t.Fatalf("positive control GET %s status = %d, want 200", saved.URL, got.Code)
	}
	if !spy.touchedMedia() {
		t.Fatalf("query spy is not wired: a valid media fetch logged no media SQL")
	}

	invalid := []string{
		"/media/abc",                        // 太短
		"/media/123",                        // 旧的数字 id
		"/media/" + strings.Repeat("a", 63), // 差 1 位
		"/media/" + strings.Repeat("a", 65), // 多 1 位
		"/media/" + strings.Repeat("z", 64), // 非十六进制
		"/media/" + strings.Repeat("A", 64), // 大写不算（只认小写）
		"/media/..%2f..%2fetc%2fpasswd",     // 路径穿越字符
	}
	for _, target := range invalid {
		spy.reset()
		rec := getWithCookies(t, srv, target, cookies)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
		}
		if spy.touchedMedia() {
			t.Errorf("GET %s queried the media table before rejecting; captured SQL: %v", target, spy.lines)
		}
	}
}

// TestMediaServeOldNumericURLLoses 是「不做旧数据迁移」的直接负例：库里有 media 行，
// 但旧的 /media/<数字 id> 形态一律 404，即使 id 对应的行确实存在。
func TestMediaServeOldNumericURLLoses(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := uploadMedia(t, srv, cookies, csrf, "x.png", "image/png", pngBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	// 行确实存在（上传者预览可读新 URL）。
	if got := getWithCookies(t, srv, saved.URL, cookies); got.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, got.Code)
	}
	// 旧形态失效：即便库里有且仅有一行，/media/1 也不得 200。
	var rows int64
	if err := db.Model(&store.Media{}).Count(&rows).Error; err != nil {
		t.Fatalf("count media: %v", err)
	}
	if rows != 1 {
		t.Fatalf("media rows = %d, want 1", rows)
	}
	if got := getWithCookies(t, srv, "/media/1", cookies); got.Code != http.StatusNotFound {
		t.Fatalf("legacy GET /media/1 status = %d, want 404 (the numeric-id URL must be dead)", got.Code)
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
// GET /media/<sha256> 作为读取入口重定向到登录页。
func TestMediaAnonymousAccessBlocked(t *testing.T) {
	srv, _, _, _, _ := newNotesServer(t)
	rec := uploadMedia(t, srv, nil, "", "a.png", "image/png", pngBody())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous upload status = %d, want 403", rec.Code)
	}
	target := "/media/" + strings.Repeat("a", 64)
	get := getWithCookies(t, srv, target, nil)
	if get.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET %s status = %d, want 303", target, get.Code)
	}
	if loc := get.Header().Get("Location"); loc != "/login" {
		t.Fatalf("anonymous GET %s Location = %q, want /login", target, loc)
	}
}
