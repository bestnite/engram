package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是匿名入口限流（/api/v1/auth/forgot-password、/api/v1/auth/register）的验收测试。
//
// 阈值固定为「IP 与目标邮箱各 5 次 / 15 分钟」。时钟注入到限流器，
// 因此窗口过期可以即时断言，不需要真实等待；请求方 IP 通过 RemoteAddr 显式指定
// （服务默认不信任任何代理，ClientIP() 取 RemoteAddr），从而把 IP 维度与邮箱维度分开验证。

// fakeRateClock 是可手动推进的注入时钟。
type fakeRateClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeRateClock() *fakeRateClock {
	return &fakeRateClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeRateClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeRateClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// anonLimiterWithClock 用默认阈值与注入时钟造一个匿名入口限流器。
func anonLimiterWithClock(clock *fakeRateClock) *auth.AnonymousLimiter {
	return auth.NewAnonymousLimiter(auth.DefaultAnonRateLimit, auth.DefaultAnonRateWindow, clock.now)
}

// postJSONFromIP 发一次带双提交 CSRF 的 JSON 写请求，并显式指定请求方 IP（RemoteAddr），
// 让「同 IP 换邮箱」与「同邮箱换 IP」两条路径可以分别构造。
func postJSONFromIP(t *testing.T, srv *Server, path, ip string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, testDoubleSubmitToken)
	req.RemoteAddr = ip + ":54321"
	req.AddCookie(&http.Cookie{Name: auth.CSRFDoubleSubmitCookieName, Value: testDoubleSubmitToken})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// mustCreateUser 直接落一个可被找回密码命中的本地账号（走真实账号服务）。
func mustCreateUser(t *testing.T, ts securityTestServer, email string) {
	t.Helper()
	name := strings.NewReplacer("@", "-", ".", "-").Replace(email)
	if _, err := ts.srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: name, Email: email, DisplayName: email,
		Password: "Sup3rSecret!", Role: store.RoleUser, Locale: "en",
	}); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
}

// TestForgotPasswordRateLimitByIP 覆盖 IP 维度：同一 IP 第 6 次被拒，且被拒那次不入 outbox。
// 每次用不同目标邮箱，邮箱维度各自停在 1，唯一可能触发的是 IP 维度。
func TestForgotPasswordRateLimitByIP(t *testing.T) {
	ts := newSecurityServer(t, true)
	ts.srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())

	const ip = "203.0.113.10"
	emails := make([]string, 6)
	for i := range emails {
		emails[i] = fmt.Sprintf("rl-ip-%d@example.com", i)
		mustCreateUser(t, ts, emails[i])
	}
	for i := 0; i < 5; i++ {
		rec := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": emails[i]})
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	before := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	if before != 5 {
		t.Fatalf("reset mails after 5 allowed requests = %d, want 5", before)
	}
	sixth := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": emails[5]})
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request from same IP status = %d, want 429 (body %s)", sixth.Code, snippet(sixth.Body.String()))
	}
	after := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	if after != before {
		t.Errorf("reset mails rose from %d to %d on the rejected request; it must not enqueue", before, after)
	}
}

// TestForgotPasswordRateLimitByEmail 覆盖目标邮箱维度：同一邮箱（不同 IP）第 6 次被拒，且不入 outbox。
func TestForgotPasswordRateLimitByEmail(t *testing.T) {
	ts := newSecurityServer(t, true)
	ts.srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())

	const email = "owner@example.com"
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("203.0.113.%d", 20+i)
		rec := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": email})
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	before := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	if before != 5 {
		t.Fatalf("reset mails after 5 allowed requests = %d, want 5", before)
	}
	sixth := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", "203.0.113.99", map[string]string{"email": email})
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request for same email status = %d, want 429 (body %s)", sixth.Code, snippet(sixth.Body.String()))
	}
	after := len(outboxByType(t, ts.db, mail.TypePasswordReset))
	if after != before {
		t.Errorf("reset mails rose from %d to %d on the rejected request; it must not enqueue", before, after)
	}
}

// TestForgotPasswordRateLimitWindowExpiry 覆盖窗口过期：注入时钟 +16 分钟后重新放行。
func TestForgotPasswordRateLimitWindowExpiry(t *testing.T) {
	ts := newSecurityServer(t, true)
	clock := newFakeRateClock()
	ts.srv.anonLimiter = anonLimiterWithClock(clock)

	const ip = "203.0.113.30"
	const email = "owner@example.com"
	for i := 0; i < 5; i++ {
		rec := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": email})
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	sixth := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": email})
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request status = %d, want 429", sixth.Code)
	}
	clock.advance(16 * time.Minute)
	seventh := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", ip, map[string]string{"email": email})
	if seventh.Code != http.StatusOK {
		t.Fatalf("request after window expiry status = %d, want 200 (body %s)", seventh.Code, snippet(seventh.Body.String()))
	}
}

// TestRegisterRateLimitByIP 覆盖注册的 IP 维度：开放策略下前 5 次成功建号，第 6 次被拒且不建号。
func TestRegisterRateLimitByIP(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))
	srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())

	const ip = "203.0.113.40"
	for i := 0; i < 5; i++ {
		rec := postJSONFromIP(t, srv, "/api/v1/auth/register", ip, map[string]string{
			"username": fmt.Sprintf("rlreg%d", i),
			"email":    fmt.Sprintf("rlreg%d@example.com", i),
			"password": "Sup3rSecret!",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("registration %d status = %d, want 200 (body %s)", i+1, rec.Code, snippet(rec.Body.String()))
		}
	}
	sixth := postJSONFromIP(t, srv, "/api/v1/auth/register", ip, map[string]string{
		"username": "rlreg5", "email": "rlreg5@example.com", "password": "Sup3rSecret!",
	})
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th registration from same IP status = %d, want 429 (body %s)", sixth.Code, snippet(sixth.Body.String()))
	}
	var count int64
	if err := db.Model(&store.User{}).Where("email = ?", "rlreg5@example.com").Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Errorf("rejected registration created %d users, want 0", count)
	}
}

// TestRegisterRateLimitByEmail 覆盖注册的邮箱维度：同一目标邮箱（不同 IP）第 6 次被拒。
// 策略用 closed，让前 5 次都不建号，仅凭响应码区分「策略拒绝」与「限流拒绝」。
func TestRegisterRateLimitByEmail(t *testing.T) {
	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "closed")))
	srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())

	const email = "blocked@example.com"
	for i := 0; i < 5; i++ {
		rec := postJSONFromIP(t, srv, "/api/v1/auth/register", fmt.Sprintf("203.0.113.%d", 50+i), map[string]string{
			"username": fmt.Sprintf("blk%d", i), "email": email, "password": "Sup3rSecret!",
		})
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d unexpectedly rate-limited; policy rejection expected first", i+1)
		}
	}
	sixth := postJSONFromIP(t, srv, "/api/v1/auth/register", "203.0.113.99", map[string]string{
		"username": "blk5", "email": email, "password": "Sup3rSecret!",
	})
	if sixth.Code != http.StatusTooManyRequests {
		t.Fatalf("6th registration for same email status = %d, want 429 (body %s)", sixth.Code, snippet(sixth.Body.String()))
	}
}

// TestAnonRateLimitFirstRequestSucceeds 是正例：两个入口的首次请求都不被误伤。
func TestAnonRateLimitFirstRequestSucceeds(t *testing.T) {
	ts := newSecurityServer(t, true)
	ts.srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())
	rec := postJSONFromIP(t, ts.srv, "/api/v1/auth/forgot-password", "203.0.113.70", map[string]string{"email": "owner@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("first forgot-password status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	srv, db := newAuthServer(t)
	seedAdminUser(t, srv)
	writeSetting(t, db, auth.SettingKeyRegistrationPolicy, string(mustJSON(t, "open")))
	srv.anonLimiter = anonLimiterWithClock(newFakeRateClock())
	reg := postJSONFromIP(t, srv, "/api/v1/auth/register", "203.0.113.71", map[string]string{
		"username": "firstok", "email": "firstok@example.com", "password": "Sup3rSecret!",
	})
	if reg.Code != http.StatusOK {
		t.Fatalf("first register status = %d, want 200 (body %s)", reg.Code, snippet(reg.Body.String()))
	}
}
