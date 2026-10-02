package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"example.com/engram/internal/auth"
)

// csrfHiddenRe 从认证页 HTML 里取镜像进表单的 CSRF token（模板输出 <input ... value="...">）。
var csrfHiddenRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// doubleSubmitFromPage 走一次 GET，返回响应下发的 csrf_double cookie 与表单里的镜像 token。
func doubleSubmitFromPage(t *testing.T, srv *Server, path string) (string, string) {
	t.Helper()
	rec := get(t, srv, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
	}
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CSRFDoubleSubmitCookieName {
			cookie = c.Value
		}
	}
	m := csrfHiddenRe.FindStringSubmatch(rec.Body.String())
	if cookie == "" || m == nil {
		t.Fatalf("GET %s did not issue a double-submit cookie/mirrored token (cookie=%q body=%s)",
			path, cookie, snippet(rec.Body.String()))
	}
	return cookie, m[1]
}

// TestPreSessionCSRFNormalBrowserFlow 是 B-13 的正向验收：真实浏览器流程（GET 拿 cookie 与
// 镜像 token，再带着它们 POST）不受影响，/setup 与 /login 都能正常走通。
func TestPreSessionCSRFNormalBrowserFlow(t *testing.T) {
	srv, _ := newAuthServer(t)

	cookie, token := doubleSubmitFromPage(t, srv, "/setup")
	rec := postForm(t, srv, "/setup", url.Values{
		"username":         {"root"},
		"email":            {"root@example.com"},
		"password":         {"Sup3rSecret!"},
		auth.CSRFFieldName: {token},
	}, []*http.Cookie{{Name: auth.CSRFDoubleSubmitCookieName, Value: cookie}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup with a valid double-submit pair = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	loginCookie, loginToken := doubleSubmitFromPage(t, srv, "/login")
	login := postForm(t, srv, "/login", url.Values{
		"username":         {"root"},
		"password":         {"Sup3rSecret!"},
		auth.CSRFFieldName: {loginToken},
	}, []*http.Cookie{{Name: auth.CSRFDoubleSubmitCookieName, Value: loginCookie}})
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login with a valid double-submit pair = %d, want 303 (body %s)", login.Code, snippet(login.Body.String()))
	}
}

// TestPreSessionCSRFRejections 是 B-13 的反面验收：缺少镜像 cookie 或值不匹配的会话前表单一律 403。
func TestPreSessionCSRFRejections(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		cookie string
		token  string
	}{
		{name: "no cookie and no token", path: "/login"},
		{name: "token without cookie", path: "/login", token: "some-mirrored-token-0123456789"},
		{name: "cookie without mirrored token", path: "/register", cookie: "a-cookie-value-0123456789ab"},
		{name: "cookie and token mismatch", path: "/register", cookie: "cookie-value-0123456789abc", token: "different-token-0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAuthServer(t)
			values := url.Values{
				"username": {"intruder"},
				"email":    {"intruder@example.com"},
				"password": {"Sup3rSecret!"},
			}
			var cookies []*http.Cookie
			if tc.cookie != "" {
				cookies = append(cookies, &http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: tc.cookie})
			}
			if tc.token != "" {
				values.Set(auth.CSRFFieldName, tc.token)
			}
			// 直接用原始请求，绕开 postForm 的自动补全，才能构造出“缺镜像 cookie”的真实请求。
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: POST %s status = %d, want 403 (body %s)", tc.name, tc.path, rec.Code, snippet(rec.Body.String()))
			}
		})
	}
}

// TestSessionCSRFTokenDoesNotAuthorizePreSessionForms 断言两条路径互相独立：会话绑定的
// token 不能替代双提交 cookie，双提交 cookie 也不能放行会话前表单之外的写请求。
func TestSessionCSRFTokenDoesNotAuthorizePreSessionForms(t *testing.T) {
	srv, _ := newAuthServer(t)
	seedAdminUser(t, srv)

	// 会话绑定的 CSRF token（服务端状态）拿不到，就构不出合法会话请求；这里验证的是
	// 双提交 cookie 与会话 token 不通用：/logout 是会话绑定路径，双提交 cookie 不放行它。
	cookie, token := doubleSubmitFromPage(t, srv, "/login")
	login := postForm(t, srv, "/login", url.Values{
		"username":         {"admin"},
		"password":         {"Sup3rSecret!"},
		auth.CSRFFieldName: {token},
	}, []*http.Cookie{{Name: auth.CSRFDoubleSubmitCookieName, Value: cookie}})
	if login.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303", login.Code)
	}
	sessionCookies := login.Result().Cookies()
	if len(sessionCookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	// 带会话但没有会话绑定的 CSRF token：必须 403，证明双提交 cookie 不是万能通行证。
	logout := postForm(t, srv, "/logout", url.Values{
		auth.CSRFFieldName: {token},
	}, sessionCookies)
	if logout.Code != http.StatusForbidden {
		t.Fatalf("POST /logout with only a double-submit token = %d, want 403", logout.Code)
	}
}
