package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

type initializeBarrier struct {
	*httptest.ResponseRecorder
	published chan string
	release   chan struct{}
}

func (w *initializeBarrier) WriteHeader(code int) {
	w.ResponseRecorder.WriteHeader(code)
	if code >= 200 {
		w.published <- w.Header().Get(mcpSessionHeader)
		<-w.release
	}
}

func (w *initializeBarrier) Write(p []byte) (int, error) {
	if w.ResponseRecorder.Code == 200 && !w.ResponseRecorder.Flushed {
		// The SDK normally writes a header first; this also catches implicit writes.
		select {
		case w.published <- w.Header().Get(mcpSessionHeader):
			<-w.release
		default:
		}
	}
	return w.ResponseRecorder.Write(p)
}

func TestInitializeOwnerPublishedBeforeResponseReturns(t *testing.T) {
	_, db, _, srv, _ := newEnvWithServer(t)
	owner := seedUser(t, db, "handshake-owner")
	other := seedUser(t, db, "handshake-other")
	key := &store.APIKey{ID: 1, Scopes: store.ScopeRead}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(rawInitBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req = req.WithContext(WithIdentity(req.Context(), owner, key))
	w := &initializeBarrier{ResponseRecorder: httptest.NewRecorder(), published: make(chan string, 1), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); srv.ServeHTTP(w, req) }()
	defer func() {
		close(w.release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("initialize did not return")
		}
	}()
	var sid string
	select {
	case sid = <-w.published:
	case <-done:
		t.Fatal("initialize returned before publishing response")
	case <-time.After(5 * time.Second):
		t.Fatal("initialize did not publish response")
	}
	if sid == "" {
		t.Fatal("initialize published no session id")
	}
	for _, tc := range []struct {
		name string
		user *store.User
		sid  string
		want int
	}{
		{"owner", owner, sid, http.StatusAccepted},
		{"foreign", other, sid, http.StatusForbidden},
		{"unknown", owner, "unknown-session", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			r.Header.Set(mcpSessionHeader, tc.sid)
			r = r.WithContext(WithIdentity(r.Context(), tc.user, key))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, r)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestInitializeResponseRegistration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header bool
		write  func(*statusWriter)
		want   bool
		status int
	}{
		{"explicit_success", true, func(w *statusWriter) { w.WriteHeader(200) }, true, 200},
		{"implicit_success", true, func(w *statusWriter) { _, _ = w.Write([]byte("ok")) }, true, 200},
		{"flush_success", true, func(w *statusWriter) { _ = w.FlushError() }, true, 200},
		{"failure", true, func(w *statusWriter) { w.WriteHeader(400) }, false, 400},
		{"missing_header", false, func(w *statusWriter) { w.WriteHeader(200) }, false, 200},
		{"informational_then_failure", true, func(w *statusWriter) { w.WriteHeader(103); w.WriteHeader(400) }, false, 400},
		{"informational_then_success", true, func(w *statusWriter) { w.WriteHeader(103); w.WriteHeader(200) }, true, 200},
		{"failure_then_success", true, func(w *statusWriter) { w.WriteHeader(400); w.WriteHeader(200) }, false, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{sessions: map[string]sessionEntry{}}
			rec := httptest.NewRecorder()
			if tc.header {
				rec.Header().Set(mcpSessionHeader, "test-session")
			}
			w := &statusWriter{ResponseWriter: rec}
			w.onSuccess = func() {
				if sid := w.Header().Get(mcpSessionHeader); sid != "" {
					s.rememberSession(sid, 1)
				}
			}
			tc.write(w)
			known, own := s.sessionOwner("test-session", 1)
			if known != tc.want || own != tc.want {
				t.Errorf("registered = %v, own = %v; want %v", known, own, tc.want)
			}
			if w.status != tc.status {
				t.Errorf("status = %d, want %d", w.status, tc.status)
			}
		})
	}
}
