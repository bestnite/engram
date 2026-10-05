package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 F20 的验收测试：管理面板里三个高影响动作——全库导出、OIDC「测试连接」、
// SMTP「测试连接」——必须写审计行，且成功与失败都要留痕。审计行不得包含
// 口令 / secret / token 等敏感值（只记动作、actor 与目标元信息）。

// 两个 canary 值只用于断言「它们不会出现在审计行里」，与任何真实凭据无关。
const (
	oidcSecretCanary   = "oidc-audit-leak-canary-secret"
	smtpPasswordCanary = "smtp-audit-leak-canary-pw"
)

// auditStr 解引用可空审计列，nil 记为空串，便于把整行拼成可搜索文本。
func auditStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// auditRowsDump 把审计行的可检索列（action / target_type / detail_json）拼成一段文本。
func auditRowsDump(rows []store.AuditLog) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "action=%s target=%s detail=%s\n", r.Action, auditStr(r.TargetType), auditStr(r.DetailJSON))
	}
	return b.String()
}

// auditRowsFor 取回指定动作的全部审计行（这里行数很少，直接全量列出再过滤）。
func auditRowsFor(t *testing.T, db *gorm.DB, action string) []store.AuditLog {
	t.Helper()
	rows, err := store.NewAuditStore(db).List(context.Background(), 500)
	if err != nil {
		t.Fatalf("list audit rows: %v", err)
	}
	var out []store.AuditLog
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

// assertNoAuditLeak 断言给定敏感值不出现在任何审计行的任一列里。
func assertNoAuditLeak(t *testing.T, db *gorm.DB, canaries ...string) {
	t.Helper()
	rows, err := store.NewAuditStore(db).List(context.Background(), 500)
	if err != nil {
		t.Fatalf("list audit rows: %v", err)
	}
	dump := auditRowsDump(rows)
	for _, c := range canaries {
		if strings.Contains(dump, c) {
			t.Errorf("audit rows leak sensitive value %q; dump =\n%s", c, dump)
		}
	}
}

// TestAdminExportWritesAuditRow 验证全库导出留痕：成功导出后恰好多一行 admin.export，
// actor 是发起导出的管理员。
func TestAdminExportWritesAuditRow(t *testing.T) {
	srv, db, adminID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	audit := store.NewAuditStore(db)

	before, err := audit.CountByAction(ctx, store.ActionAdminExport)
	if err != nil {
		t.Fatalf("count admin.export audits: %v", err)
	}
	rec := getWithCookies(t, srv, "/admin/export", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/export = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after, err := audit.CountByAction(ctx, store.ActionAdminExport)
	if err != nil {
		t.Fatalf("count admin.export audits: %v", err)
	}
	if after != before+1 {
		t.Fatalf("admin.export audit rows = %d, want %d", after, before+1)
	}

	rows := auditRowsFor(t, db, store.ActionAdminExport)
	if len(rows) != 1 {
		t.Fatalf("admin.export rows = %d, want 1", len(rows))
	}
	if rows[0].UserID == nil || *rows[0].UserID != adminID {
		t.Errorf("admin.export actor = %v, want user %d", rows[0].UserID, adminID)
	}
	if tt := auditStr(rows[0].TargetType); tt != "database" {
		t.Errorf("admin.export target_type = %q, want \"database\"", tt)
	}
}

// TestAdminOIDCTestWritesAuditOnSuccessAndFailure 验证 OIDC「测试连接」成功与失败各写一行，
// actor 正确，且审计行不含提交的 client secret。
func TestAdminOIDCTestWritesAuditOnSuccessAndFailure(t *testing.T) {
	srv, db, adminID, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// 成功路径：stub provider 提供发现文档。
	okStub := newStubOIDC(t)
	okRec := postForm(t, srv, "/admin/oidc/test", url.Values{
		"csrf_token":         {csrf},
		"oidc.issuer":        {okStub.srv.URL},
		"oidc.client_secret": {oidcSecretCanary},
	}, cookies)
	if okRec.Code != http.StatusOK {
		t.Fatalf("OIDC test (success) = %d, want 200 (body %s)", okRec.Code, snippet(okRec.Body.String()))
	}

	// 失败路径：一个总是返回 404 的 provider。
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("provider-says-no-discovery"))
	}))
	t.Cleanup(broken.Close)
	badRec := postForm(t, srv, "/admin/oidc/test", url.Values{
		"csrf_token":         {csrf},
		"oidc.issuer":        {broken.URL},
		"oidc.client_secret": {oidcSecretCanary},
	}, cookies)
	if badRec.Code != http.StatusOK {
		t.Fatalf("OIDC test (failure) = %d, want 200 (body %s)", badRec.Code, snippet(badRec.Body.String()))
	}

	n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionAdminOIDCTest)
	if err != nil {
		t.Fatalf("count admin.oidc_test audits: %v", err)
	}
	if n != 2 {
		t.Fatalf("admin.oidc_test audit rows = %d, want 2 (success + failure)", n)
	}
	for _, r := range auditRowsFor(t, db, store.ActionAdminOIDCTest) {
		if r.UserID == nil || *r.UserID != adminID {
			t.Errorf("admin.oidc_test actor = %v, want user %d", r.UserID, adminID)
		}
		if tt := auditStr(r.TargetType); tt != "oidc" {
			t.Errorf("admin.oidc_test target_type = %q, want \"oidc\"", tt)
		}
	}
	if dump := auditRowsDump(auditRowsFor(t, db, store.ActionAdminOIDCTest)); !strings.Contains(dump, okStub.srv.URL) {
		t.Errorf("admin.oidc_test detail does not record the issuer; dump =\n%s", dump)
	}
	assertNoAuditLeak(t, db, oidcSecretCanary)
}

// TestAdminSMTPTestWritesAuditOnSuccessAndFailure 验证 SMTP「测试连接」成功与失败各写一行，
// actor 正确，且审计行不含提交的 SMTP 口令。
func TestAdminSMTPTestWritesAuditOnSuccessAndFailure(t *testing.T) {
	srv, db, adminID, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// 成功路径：进程内的假 SMTP 服务（不设账号口令——假服务不实现 AUTH 握手）。
	addr := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	okRec := postForm(t, srv, "/admin/smtp/test", url.Values{
		"csrf_token":    {csrf},
		"smtp_host":     {host},
		"smtp_port":     {port},
		"smtp_from":     {"no-reply@example.com"},
		"smtp_tls_mode": {"none"},
	}, cookies)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), "连接成功") {
		t.Fatalf("SMTP test (success): status=%d body=%s", okRec.Code, snippet(okRec.Body.String()))
	}

	// 失败路径：一个当前无人监听的本地地址。
	dead := deadAddr(t)
	dhost, dport, _ := net.SplitHostPort(dead)
	badRec := postForm(t, srv, "/admin/smtp/test", url.Values{
		"csrf_token":    {csrf},
		"smtp_host":     {dhost},
		"smtp_port":     {dport},
		"smtp_from":     {"no-reply@example.com"},
		"smtp_password": {smtpPasswordCanary},
		"smtp_tls_mode": {"none"},
	}, cookies)
	if badRec.Code != http.StatusOK || !strings.Contains(badRec.Body.String(), "连接失败") {
		t.Fatalf("SMTP test (failure): status=%d body=%s", badRec.Code, snippet(badRec.Body.String()))
	}

	n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionAdminSMTPTest)
	if err != nil {
		t.Fatalf("count admin.smtp_test audits: %v", err)
	}
	if n != 2 {
		t.Fatalf("admin.smtp_test audit rows = %d, want 2 (success + failure)", n)
	}
	seenHost := false
	for _, r := range auditRowsFor(t, db, store.ActionAdminSMTPTest) {
		if r.UserID == nil || *r.UserID != adminID {
			t.Errorf("admin.smtp_test actor = %v, want user %d", r.UserID, adminID)
		}
		if tt := auditStr(r.TargetType); tt != "smtp" {
			t.Errorf("admin.smtp_test target_type = %q, want \"smtp\"", tt)
		}
		if strings.Contains(auditStr(r.DetailJSON), host) {
			seenHost = true
		}
	}
	if !seenHost {
		t.Errorf("admin.smtp_test detail does not record the tested host %q", host)
	}
	assertNoAuditLeak(t, db, smtpPasswordCanary)
}
