package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/urlfetch"
)

// 直链导入端点（POST /api/v1/decks/import-url）的验收。
//
// 下载器用受控实现替换：端点测试只关心「拒绝时是否发起了下载」「错误如何映射」
// 「成功后是否真的导入了包」；下载器自身的传输行为由 internal/urlfetch 的真实 HTTPS 用例覆盖。

// stubFetcher 是受控的 PackageFetcher：返回预设字节或预设错误，并记录调用。
type stubFetcher struct {
	body  []byte
	err   error
	calls int
	urls  []string
}

func (f *stubFetcher) Fetch(_ context.Context, rawURL string, _ int64) (io.ReadCloser, error) {
	f.calls++
	f.urls = append(f.urls, rawURL)
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

// postImportURL 发一个带 bearer key 的 JSON 直链导入请求。
func postImportURL(t *testing.T, env *testEnv, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/import-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	env.router().ServeHTTP(rec, req)
	return rec
}

func decodeErrorCode(t *testing.T, raw []byte) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, raw)
	}
	return resp.Error.Code
}

func TestImportURLRequiresWriteScopeAndDoesNotFetch(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_readonly", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	rec := postImportURL(t, env, k.Plaintext, `{"url":"https://example.com/d.edeck","target":"new_deck"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-scope import-url = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeScopeRequired {
		t.Errorf("error code = %q, want %q", code, CodeScopeRequired)
	}
	if f.calls != 0 {
		t.Errorf("fetcher was called %d times on a scope denial, want 0", f.calls)
	}
}

func TestImportURLSessionRequiresCSRF(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_session", store.RoleUser)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	mgr, err := auth.NewSessionManager(
		store.NewUserStore(env.db), store.NewSessionStore(env.db),
		auth.SessionConfig{Secret: []byte("test-secret"), Now: func() time.Time { return env.now }},
	)
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	r := gin.New()
	r.Use(mgr.Middleware())
	env.api.Register(r)

	// 建一个真实会话并取出签名 cookie。
	cookieRec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(cookieRec)
	sess, err := mgr.StartSession(context.Background(), c, user.ID)
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	cookies := cookieRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("StartSession() set no cookie")
	}

	send := func(csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/decks/import-url",
			strings.NewReader(`{"url":"https://example.com/d.edeck","target":"new_deck"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookies[0])
		if csrf != "" {
			req.Header.Set(auth.CSRFHeaderName, csrf)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := send("")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("session import-url without CSRF = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeCSRFFailed {
		t.Errorf("error code = %q, want %q", code, CodeCSRFFailed)
	}
	if f.calls != 0 {
		t.Errorf("fetcher was called %d times on a CSRF denial, want 0", f.calls)
	}

	ok := send(sess.CSRFToken)
	if ok.Code != http.StatusOK {
		t.Fatalf("session import-url with CSRF = %d, want 200 (body %s)", ok.Code, ok.Body.String())
	}
	if f.calls != 1 {
		t.Errorf("fetcher calls = %d, want 1 after a valid session import", f.calls)
	}
}

func TestImportURLDeniedRoleDoesNotFetch(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "url_owner", store.RoleUser)
	intruder := seedUser(t, env.db, "url_intruder", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	k := seedKey(t, env.keys, intruder.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	body := fmt.Sprintf(`{"url":"https://example.com/d.edeck","target":"into_deck:%s"}`, deck.PublicID)
	rec := postImportURL(t, env, k.Plaintext, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("import-url into a deck without access = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if f.calls != 0 {
		t.Errorf("fetcher was called %d times on a role denial, want 0 (no outbound request before the check)", f.calls)
	}
}

func TestImportURLSuccess(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_success", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	rec := postImportURL(t, env, k.Plaintext, `{"url":"https://example.com/deck.edeck","target":"new_deck"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("import-url = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	t.Logf("import-url response: %s", rec.Body.String())
	var report store.PackageImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("report is not JSON: %v (%s)", err, rec.Body.String())
	}
	if report.DryRun || report.NotesCreated != 1 || report.DeckPublicID == "" {
		t.Errorf("report = %+v, want a real import with one note and a deck id", report)
	}
	if f.calls != 1 || len(f.urls) != 1 || f.urls[0] != "https://example.com/deck.edeck" {
		t.Errorf("fetcher calls/urls = %d/%v, want one call to the given url", f.calls, f.urls)
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 1 {
		t.Errorf("decks = %d, want 1 after a successful import", decks)
	}
}

func TestImportURLCorruptPackageIsRejected(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_corrupt", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	// 下载成功但内容不是卡组包：包解析必须在写任何东西之前整包拒绝。
	f := &stubFetcher{body: []byte("this is not a zip archive")}
	env.api.fetcher = f

	rec := postImportURL(t, env, k.Plaintext, `{"url":"https://example.com/bad.edeck","target":"new_deck"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("corrupt package = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != store.CodePackageBadFormat {
		t.Errorf("code = %q, want %q", code, store.CodePackageBadFormat)
	}
	if f.calls != 1 {
		t.Errorf("fetcher calls = %d, want 1 (the download happened, the parse rejected it)", f.calls)
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Errorf("decks = %d, want 0 after a rejected package", decks)
	}
}

func TestImportURLDryRunWritesNothing(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_dry", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	rec := postImportURL(t, env, k.Plaintext, `{"url":"https://example.com/deck.edeck","target":"new_deck","dry_run":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry-run import-url = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var report store.PackageImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("report is not JSON: %v", err)
	}
	if !report.DryRun || report.NotesCreated != 1 {
		t.Errorf("report = %+v, want dry_run=true notes_created=1", report)
	}
	var decks int64
	if err := env.db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Errorf("decks = %d, want 0 after a dry run", decks)
	}
}

func TestImportURLFetchErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		fetchErr   error
		wantStatus int
		wantCode   string
	}{
		{"invalid url", &urlfetch.Error{Kind: urlfetch.KindInvalidURL, Reason: "only https"}, http.StatusBadRequest, CodeImportURLInvalid},
		{"blocked host", &urlfetch.Error{Kind: urlfetch.KindBlockedHost, Reason: "private"}, http.StatusBadRequest, CodeImportURLBlocked},
		{"not a package", &urlfetch.Error{Kind: urlfetch.KindNotPackage, Reason: "html"}, http.StatusBadRequest, CodeImportURLNotPackage},
		{"too large", &urlfetch.Error{Kind: urlfetch.KindTooLarge, Reason: "over"}, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge},
		{"fetch failed", &urlfetch.Error{Kind: urlfetch.KindFetchFailed, Reason: "timeout"}, http.StatusBadGateway, CodeImportURLFetchFailed},
		{"unclassified", io.ErrUnexpectedEOF, http.StatusBadGateway, CodeImportURLFetchFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, safeName("url_err_"+tc.name), store.RoleUser)
			k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
			env.api.fetcher = &stubFetcher{err: tc.fetchErr}

			rec := postImportURL(t, env, k.Plaintext, `{"url":"https://example.com/deck.edeck","target":"new_deck"}`)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec.Body.Bytes()); code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
		})
	}
}

func TestImportURLRejectsOversizedJSONBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_bigjson", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	huge := `{"url":"https://example.com/` + strings.Repeat("a", 80<<10) + `.edeck"}`
	rec := postImportURL(t, env, k.Plaintext, huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON body = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if f.calls != 0 {
		t.Errorf("fetcher was called %d times for a rejected body, want 0", f.calls)
	}
}

func TestImportURLIsRateLimited(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_ratelimit", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	body := `{"url":"https://example.com/deck.edeck","target":"new_deck","dry_run":true}`
	for i := 0; i < importURLRateLimit; i++ {
		if rec := postImportURL(t, env, k.Plaintext, body); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 (body %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if f.calls != importURLRateLimit {
		t.Fatalf("fetcher calls = %d, want %d after %d allowed requests", f.calls, importURLRateLimit, importURLRateLimit)
	}
	rec := postImportURL(t, env, k.Plaintext, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d = %d, want 429 (body %s)", importURLRateLimit+1, rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeRateLimited {
		t.Errorf("error code = %q, want %q", code, CodeRateLimited)
	}
	// 429 必须带 Retry-After，客户端据此在窗口后重试。
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want \"60\"", got)
	}
	if f.calls != importURLRateLimit {
		t.Errorf("fetcher calls = %d after the rate-limited request, want %d (no download once the quota is spent)", f.calls, importURLRateLimit)
	}
}

// TestImportURLRateLimitPoolIsSharedWithService 证明限流在 service 层：先用 REST handler
// 用尽配额，再直接调用 ImportDeckPackageURL（MCP 工具走的同一入口）也必须被拦下且不再下载。
func TestImportURLRateLimitPoolIsSharedWithService(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_pool_service", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	f := &stubFetcher{body: minimalPackageZip(t)}
	env.api.fetcher = f

	body := `{"url":"https://example.com/deck.edeck","target":"new_deck","dry_run":true}`
	for i := 0; i < importURLRateLimit; i++ {
		if rec := postImportURL(t, env, k.Plaintext, body); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 (body %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	_, err := env.api.ImportDeckPackageURL(context.Background(), user, nil,
		"https://example.com/deck.edeck", store.PackageImportOptions{Target: "new_deck", DryRun: true})
	var se *ServiceError
	if !errors.As(err, &se) || se.Code != CodeRateLimited {
		t.Fatalf("service call after the quota is spent = %v, want CodeRateLimited", err)
	}
	if f.calls != importURLRateLimit {
		t.Errorf("fetcher calls = %d, want %d (a rate-limited service call must not download)", f.calls, importURLRateLimit)
	}
}

// TestImportURLMapsRealFetcherErrorsThroughHTTP 用真实下载器（无网络）走一遍端点，
// 证明端点把 internal/urlfetch 的分类错误映射成稳定 code，而不是只对 stub 生效。
func TestImportURLMapsRealFetcherErrorsThroughHTTP(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "url_real_fetch", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	// 保留 New() 建的生产下载器；对私网/非 HTTPS 地址的拒绝发生在任何网络调用之前。
	rec := postImportURL(t, env, k.Plaintext, `{"url":"http://example.com/deck.edeck","target":"new_deck"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-https url = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeImportURLInvalid {
		t.Errorf("code = %q, want %q", code, CodeImportURLInvalid)
	}

	rec = postImportURL(t, env, k.Plaintext, `{"url":"https://127.0.0.1/deck.edeck","target":"new_deck"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("loopback url = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeImportURLBlocked {
		t.Errorf("code = %q, want %q", code, CodeImportURLBlocked)
	}
}
