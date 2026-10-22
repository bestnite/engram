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

// uploadMedia submits an upload named file as multipart; declared becomes that part's Content-Type.
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

// textProto is the constructor for multipart part headers, avoiding reliance on the deprecated mime/multipart helper types.
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

// TestMediaUploadDedupeAndServe is the main acceptance: the same file uploaded twice is stored once,
// GET /media/<sha256> carries an ETag and immutable caching, and an ETag hit returns 304.
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

	// Proxy read: ETag = sha256, Cache-Control immutable.
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

	// ETag hit -> 304.
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

// dbQuerySpy records the SQL GORM emits, proving "a request with an illegal sha does not query the database".
type dbQuerySpy struct {
	mu    sync.Mutex
	lines []string
}

func (s *dbQuerySpy) Printf(msg string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// logger.Writer receives a format string plus args, so it must be formatted here, otherwise what gets stored is "%s".
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
		// Only the backtick-quoted table name counts; source file paths in the log (.../media-l1/...) also contain "media",
		// so matching a bare "media" would misread "did not query" as "did query".
		if strings.Contains(l, "`media`") {
			return true
		}
	}
	return false
}

// TestMediaServeRejectsNonShaWithoutDBQuery covers shape validation: parameters shorter/longer than 64 characters, containing non-hex
// characters, a legacy numeric id, or path-traversal characters all 404, and the media table is not touched before that 404.
func TestMediaServeRejectsNonShaWithoutDBQuery(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	// Seed one real media row first: if the handler queried the database with an illegal parameter, it could hit it and return 200.
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

	// Positive control: one legitimate media read must be seen by the spy. Otherwise the "no media SQL" below is a lie
	// (the spy was never wired up, rather than the handler not querying).
	spy.reset()
	if got := getWithCookies(t, srv, saved.URL, cookies); got.Code != http.StatusOK {
		t.Fatalf("positive control GET %s status = %d, want 200", saved.URL, got.Code)
	}
	if !spy.touchedMedia() {
		t.Fatalf("query spy is not wired: a valid media fetch logged no media SQL")
	}

	invalid := []string{
		"/media/abc",                        // too short
		"/media/123",                        // legacy numeric id
		"/media/" + strings.Repeat("a", 63), // one character short
		"/media/" + strings.Repeat("a", 65), // one character too many
		"/media/" + strings.Repeat("z", 64), // not hex
		"/media/" + strings.Repeat("A", 64), // uppercase does not count (lowercase only)
		"/media/..%2f..%2fetc%2fpasswd",     // path traversal characters
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

// TestMediaServeOldNumericURLLoses is the direct negative case for "no data migration": the database holds a media row,
// yet the old /media/<numeric id> form always 404s, even when the row for that id really exists.
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
	// The row really exists (the uploader preview can read the new URL).
	if got := getWithCookies(t, srv, saved.URL, cookies); got.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, got.Code)
	}
	// The old form is dead: even with exactly one row in the database, /media/1 must not return 200.
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

// TestMediaUploadRejectsOversized covers the admin-configured limit taking effect: anything over media_max_bytes is refused.
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

// TestMediaUploadRejectsMagicMismatch covers a magic number that does not match the declaration being refused with a stable error code.
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

// TestMediaAnonymousAccessBlocked covers the anonymous boundary: POST is stopped by CSRF (403),
// and GET /media/<sha256>, as a read entry point, redirects to the login page.
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
