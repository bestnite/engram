package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestMcpRefusalsCarryStableCodes asserts the two refusals in ServeHTTP answer the shared JSON error
// envelope with the same stable codes REST uses, so an MCP client can branch on the code instead of
// parsing a plain-text body.
func TestMcpRefusalsCarryStableCodes(t *testing.T) {
	_, db, keys, srv, ts := newEnvWithServer(t)
	a := seedUser(t, db, "refusal_owner")
	b := seedUser(t, db, "refusal_other")
	keyA := newKey(t, keys, a.ID, []string{store.ScopeRead})
	keyB := newKey(t, keys, b.ID, []string{store.ScopeRead})

	// No identity in the request context: ServeHTTP is the gate behind the authenticated mount.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	srv.ServeHTTP(rec, req)
	assertRefusalCode(t, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), http.StatusUnauthorized, "unauthorized")

	// A registered session presented by another account's key.
	sid := rawInitialize(t, ts.URL, keyA)
	status, hdr, body := rawPost(t, ts.URL, keyB, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	assertRefusalCode(t, status, hdr.Get("Content-Type"), body, http.StatusForbidden, "forbidden")
}

// assertRefusalCode checks one refusal's status, JSON content type and envelope code.
func assertRefusalCode(t *testing.T, status int, contentType, body string, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("refusal status = %d, want %d (body %q)", status, wantStatus, body)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("refusal content type = %q, want application/json", contentType)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("refusal body is not JSON (%v): %q", err, body)
	}
	if envelope.Error.Code != wantCode {
		t.Errorf("refusal code = %q, want %q (body %q)", envelope.Error.Code, wantCode, body)
	}
}
