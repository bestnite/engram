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

// This file is the acceptance test for anonymous entry rate limiting (/api/v1/auth/forgot-password, /api/v1/auth/register).
//
// The threshold is fixed at "5 times each for IP and target mailbox / 15 minutes". The clock is injected into the limiter,
// so window expiry can be asserted immediately without real waiting; the requester IP is specified explicitly via RemoteAddr
// (the service trusts no proxy by default and ClientIP() takes RemoteAddr), letting the IP dimension and the mailbox dimension be verified separately.

// fakeRateClock is an injected clock that can be advanced manually.
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

// anonLimiterWithClock builds an anonymous entry limiter with the default threshold and an injected clock.
func anonLimiterWithClock(clock *fakeRateClock) *auth.AnonymousLimiter {
	return auth.NewAnonymousLimiter(auth.DefaultAnonRateLimit, auth.DefaultAnonRateWindow, clock.now)
}

// postJSONFromIP sends one JSON write request with double-submit CSRF, specifying the requester IP explicitly (RemoteAddr),
// so the "same IP, different mailbox" and "same mailbox, different IP" paths can be constructed separately.
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

// mustCreateUser directly persists a local account that forgot-password can hit (through the real account service).
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

// TestForgotPasswordRateLimitByIP covers the IP dimension: the 6th request from the same IP is rejected, and that rejected one does not enter the outbox.
// Each request uses a different target mailbox, so each mailbox dimension stays at 1 and the only one that can trigger is the IP dimension.
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

// TestForgotPasswordRateLimitByEmail covers the target mailbox dimension: the 6th request for the same mailbox (different IPs) is rejected and does not enter the outbox.
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

// TestForgotPasswordRateLimitWindowExpiry covers window expiry: after advancing the injected clock by 16 minutes requests are allowed again.
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

// TestRegisterRateLimitByIP covers registration's IP dimension: under an open policy the first 5 create accounts, the 6th is rejected and creates none.
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

// TestRegisterRateLimitByEmail covers registration's mailbox dimension: the 6th request for the same target mailbox (different IPs) is rejected.
// The policy is closed so none of the first 5 create an account, letting the response code alone distinguish "policy rejection" from "rate-limit rejection".
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

// TestAnonRateLimitFirstRequestSucceeds is the positive case: the first request on both entries is not wrongly blocked.
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
