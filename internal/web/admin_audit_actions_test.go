package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is an acceptance test: two high-impact actions in the admin panel — OIDC "test connection" and
// SMTP "test connection" — must write audit rows, and both success and failure must leave a trace. Audit rows must not contain
// sensitive values such as passwords / secrets / tokens (only the action, actor and target metadata are recorded).
//
// After the SSR endpoints were removed, test connection goes through the SPA's JSON endpoints (/api/v1/admin/{oidc,smtp}/test),
// with audit semantics and response shape unchanged from before the migration.

// The two canary values are only used to assert "they do not appear in the audit rows" and are unrelated to any real credential.
const (
	oidcSecretCanary   = "oidc-audit-leak-canary-secret"
	smtpPasswordCanary = "smtp-audit-leak-canary-pw"
)

// auditStr dereferences a nullable audit column, treating nil as an empty string, so a whole row can be assembled into searchable text.
func auditStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// auditRowsDump assembles an audit row's searchable columns (action / target_type / detail_json) into a single piece of text.
func auditRowsDump(rows []store.AuditLog) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "action=%s target=%s detail=%s\n", r.Action, auditStr(r.TargetType), auditStr(r.DetailJSON))
	}
	return b.String()
}

// auditRowsFor retrieves all audit rows for the given action (there are very few rows here, so list them all and filter).
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

// assertNoAuditLeak asserts that the given sensitive value appears in no column of any audit row.
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

// decodeAdminTestResult decodes the test-connection response body.
func decodeAdminTestResult(t *testing.T, rec *httptest.ResponseRecorder) adminTestResult {
	t.Helper()
	var result adminTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode test result: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return result
}

// TestAdminOIDCTestWritesAuditOnSuccessAndFailure verifies that OIDC "test connection" writes one row on success and one on failure,
// with the correct actor, and that the audit rows do not contain the submitted client secret.
func TestAdminOIDCTestWritesAuditOnSuccessAndFailure(t *testing.T) {
	srv, db, adminID, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// Success path: the stub provider serves a discovery document.
	okStub := newStubOIDC(t)
	okRec := adminPostJSON(t, srv, "/api/v1/admin/oidc/test", adminOIDCRequest{
		Issuer:       okStub.srv.URL,
		ClientSecret: oidcSecretCanary,
	}, cookies, csrf)
	if okRec.Code != http.StatusOK {
		t.Fatalf("OIDC test (success) = %d, want 200 (body %s)", okRec.Code, snippet(okRec.Body.String()))
	}
	if got := decodeAdminTestResult(t, okRec); !got.OK {
		t.Fatalf("OIDC test (success) result = %+v, want ok", got)
	}

	// Failure path: a provider that always returns 404.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("provider-says-no-discovery"))
	}))
	t.Cleanup(broken.Close)
	badRec := adminPostJSON(t, srv, "/api/v1/admin/oidc/test", adminOIDCRequest{
		Issuer:       broken.URL,
		ClientSecret: oidcSecretCanary,
	}, cookies, csrf)
	if badRec.Code != http.StatusOK {
		t.Fatalf("OIDC test (failure) = %d, want 200 (body %s)", badRec.Code, snippet(badRec.Body.String()))
	}
	if got := decodeAdminTestResult(t, badRec); got.OK || got.Message == "" {
		t.Fatalf("OIDC test (failure) result = %+v, want failure with provider error text", got)
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

// TestAdminSMTPTestWritesAuditOnSuccessAndFailure verifies that SMTP "test connection" writes one row on success and one on failure,
// with the correct actor, and that the audit rows do not contain the submitted SMTP password.
func TestAdminSMTPTestWritesAuditOnSuccessAndFailure(t *testing.T) {
	srv, db, adminID, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// Success path: an in-process fake SMTP server (no account password set — the fake does not implement the AUTH handshake).
	addr := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)
	okRec := adminPostJSON(t, srv, "/api/v1/admin/smtp/test", adminSMTPRequest{
		Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none",
	}, cookies, csrf)
	if okRec.Code != http.StatusOK {
		t.Fatalf("SMTP test (success) = %d, want 200 (body %s)", okRec.Code, snippet(okRec.Body.String()))
	}
	if got := decodeAdminTestResult(t, okRec); !got.OK {
		t.Fatalf("SMTP test (success) result = %+v, want ok", got)
	}

	// Failure path: a local address nobody is currently listening on.
	dead := deadAddr(t)
	dhost, dport, _ := net.SplitHostPort(dead)
	badRec := adminPostJSON(t, srv, "/api/v1/admin/smtp/test", adminSMTPRequest{
		Host: dhost, Port: dport, From: "no-reply@example.com",
		Password: smtpPasswordCanary, TLSMode: "none",
	}, cookies, csrf)
	if badRec.Code != http.StatusOK {
		t.Fatalf("SMTP test (failure) = %d, want 200 (body %s)", badRec.Code, snippet(badRec.Body.String()))
	}
	if got := decodeAdminTestResult(t, badRec); got.OK || got.Message == "" {
		t.Fatalf("SMTP test (failure) result = %+v, want failure with server error text", got)
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
