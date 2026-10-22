package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance for the SPA OIDC login entry probe endpoint (internal/web/oidc_api.go).
//
// Covers: enabled=false when off by default, enabled=true when the configuration is complete and usable, and a response that never contains credentials
// such as issuer / client_id; start_url comes from the same source as the SSR login page button.

// TestOIDCEntryProbe asserts the probe endpoint reflects only "available or not" and never echoes any configuration credential.
func TestOIDCEntryProbe(t *testing.T) {
	srv, db := newAuthServer(t)

	type probe struct {
		Enabled  bool   `json:"enabled"`
		StartURL string `json:"start_url"`
	}

	// Off by default: enabled=false, and start_url is still always given (the frontend decides from it whether to render the entry).
	rec := get(t, srv, "/api/v1/auth/oidc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/oidc status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var off probe
	decodeJSON(t, rec, &off)
	if off.Enabled {
		t.Errorf("oidc enabled = true by default, want false")
	}
	if off.StartURL != "/auth/oidc/start" {
		t.Errorf("start_url = %q, want /auth/oidc/start", off.StartURL)
	}

	// Configuration complete and usable: enabled=true.
	now := time.Now().UTC()
	for k, v := range map[string]string{
		auth.SettingKeyOIDCEnabled:  "true",
		auth.SettingKeyOIDCIssuer:   "https://issuer.example.com",
		auth.SettingKeyOIDCClientID: "client-abc",
	} {
		if err := store.PutSetting(context.Background(), db, k, v, nil, now); err != nil {
			t.Fatalf("PutSetting(%s): %v", k, err)
		}
	}
	on := get(t, srv, "/api/v1/auth/oidc", nil)
	var enabled probe
	decodeJSON(t, on, &enabled)
	if !enabled.Enabled {
		t.Errorf("oidc enabled = false after a complete config, want true")
	}
	if body := on.Body.String(); strings.Contains(body, "issuer.example.com") || strings.Contains(body, "client-abc") {
		t.Errorf("oidc probe leaked configuration: %s", snippet(body))
	}
}
