package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/store"
)

// TestMutationsWriteExactlyOneAuditRow 是 M1-10 的接线验收：注册、登录、登出各写一行，
// action 字符串与 store 中的常量一致。
func TestMutationsWriteExactlyOneAuditRow(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	if code := postForm(t, srv, "/register", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil).Code; code != http.StatusSeeOther {
		t.Fatalf("POST /register status = %d, want 303", code)
	}

	ok := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", ok.Code)
	}
	// 从服务端取会话绑定的 CSRF token（页面之外的通道）。
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	logout := postForm(t, srv, "/logout",
		url.Values{auth.CSRFFieldName: {sess.CSRFToken}}, ok.Result().Cookies())
	if logout.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", logout.Code)
	}

	audit := store.NewAuditStore(db)
	for action, want := range map[string]int64{
		store.ActionUserCreate:         1,
		store.ActionUserLoginSucceeded: 1,
		store.ActionUserLogout:         1,
	} {
		got, err := audit.CountByAction(ctx, action)
		if err != nil {
			t.Fatalf("CountByAction(%q) error = %v", action, err)
		}
		if got != want {
			t.Errorf("audit rows for %q = %d, want %d", action, got, want)
		}
	}

	createRow, err := audit.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(createRow) != 3 {
		t.Fatalf("total audit rows = %d, want 3", len(createRow))
	}
}

// TestLoginFailureIsAuditedAndLimiterResets 是 M1-9 的接线验收：失败写审计并累加限流，
// 成功登录后限流重置。
func TestLoginFailureIsAuditedAndLimiterResets(t *testing.T) {
	srv, db := newAuthServer(t)
	ctx := context.Background()

	if code := postForm(t, srv, "/register", url.Values{
		"username": {"admin"},
		"email":    {"admin@example.com"},
		"password": {"Sup3rSecret!"},
	}, nil).Code; code != http.StatusSeeOther {
		t.Fatalf("POST /register status = %d, want 303", code)
	}

	for i := 0; i < 3; i++ {
		rec := postForm(t, srv, "/login", url.Values{
			"username": {"admin"},
			"password": {"WrongPassword!"},
		}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login #%d status = %d, want 401", i+1, rec.Code)
		}
	}

	audit := store.NewAuditStore(db)
	if n, err := audit.CountByAction(ctx, store.ActionUserLoginFailed); err != nil || n != 3 {
		t.Fatalf("user.login_failed audit rows = (%d, %v), want (3, nil)", n, err)
	}
	// 账号维度已累计失败：下一次尝试必须等待（IP 传空串只查账号维度）。
	if d, err := srv.loginLimiter.Wait(ctx, "admin", ""); err != nil || d <= 0 {
		t.Fatalf("limiter delay before a successful login = (%v, %v), want > 0", d, err)
	}

	ok := postForm(t, srv, "/login", url.Values{
		"username": {"admin"},
		"password": {"Sup3rSecret!"},
	}, nil)
	if ok.Code != http.StatusSeeOther {
		t.Fatalf("successful POST /login status = %d, want 303", ok.Code)
	}
	if d, err := srv.loginLimiter.Wait(ctx, "admin", ""); err != nil || d != 0 {
		t.Fatalf("limiter delay after a successful login = (%v, %v), want (0, nil)", d, err)
	}
	if n, err := audit.CountByAction(ctx, store.ActionUserLoginSucceeded); err != nil || n != 1 {
		t.Fatalf("user.login_succeeded audit rows = (%d, %v), want (1, nil)", n, err)
	}
}
