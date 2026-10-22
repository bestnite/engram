package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
)

// TestPreSessionCSRFCookieSecureMatchesSiteScheme asserts the Secure flag of the pre-login double-submit cookie is decided by
// BASE_URL's scheme, not by the request's own TLS state.
//
// Production is "Caddy terminates TLS → the app only receives plaintext http", so judging by the request TLS would make the
// csrf_double issued by the pre-login form on the live site miss Secure; browsers do not send Secure cookies back over http, and conversely an http dev site
// marked Secure would break the pre-login form the same way. httptest.NewRequest does not go through a real listener, so TLS is always nil,
// which happens to simulate plaintext ingress after a reverse proxy terminates TLS.
func TestPreSessionCSRFCookieSecureMatchesSiteScheme(t *testing.T) {
	cases := []struct {
		name       string
		baseURL    string
		wantSecure bool
	}{
		{name: "https site behind tls-terminating proxy", baseURL: "https://cards.example.com", wantSecure: true},
		{name: "https site with trailing slash", baseURL: "https://cards.example.com/", wantSecure: true},
		{name: "http development site", baseURL: "http://localhost:8080", wantSecure: false},
		{name: "unset base url (development default)", baseURL: "", wantSecure: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAuthServer(t)
			srv.baseURL = tc.baseURL

			rec := get(t, srv, "/login", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /login status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
			}
			var cookie *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == auth.CSRFDoubleSubmitCookieName {
					cookie = c
				}
			}
			if cookie == nil {
				t.Fatalf("GET /login did not set the %s cookie", auth.CSRFDoubleSubmitCookieName)
			}
			if cookie.Secure != tc.wantSecure {
				t.Errorf("csrf_double Secure = %v, want %v (baseURL %q)", cookie.Secure, tc.wantSecure, tc.baseURL)
			}
			// Attributes other than Secure must stay unchanged (HttpOnly + SameSite=Lax + Path=/).
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
				t.Errorf("cookie flags = HttpOnly:%v SameSite:%v Path:%q, want HttpOnly:true SameSite:Lax Path:/",
					cookie.HttpOnly, cookie.SameSite, cookie.Path)
			}
		})
	}
}
