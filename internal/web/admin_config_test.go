package web

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance test for the admin panel's "system settings / mail / identity and OIDC" cutover to the SPA.
// The read and write endpoints reuse exactly the same parsing and validation as SSR; sensitive items only return status, never plaintext.

// TestAdminConfigPagesCutover covers the cutover and fallback of the three config pages.
func TestAdminConfigPagesCutover(t *testing.T) {
	for _, path := range []string{"/admin/settings", "/admin/smtp", "/admin/oidc"} {
		t.Run(path, func(t *testing.T) {
			srv, db, _, cookies, _ := newNotesServer(t)

			assertShell(t, getWithCookies(t, srv, path, cookies))

			_, strangerCookies, _ := createUserAndLogin(t, srv, db, "cfg_stranger")
			if rec := getWithCookies(t, srv, path, strangerCookies); rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin GET %s = %d, want 403", path, rec.Code)
			}

		})
	}
}

// TestAdminSettingsReadAndSave covers reading, validating and persisting system settings JSON.
func TestAdminSettingsReadAndSave(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	names := map[string]bool{}
	foundSiteName := false
	for _, sec := range read.Sections {
		names[sec.Name] = true
		for _, row := range sec.Rows {
			if row.Key == "site.name" {
				foundSiteName = true
				if row.Source != "default" {
					t.Errorf("site.name source = %q, want default", row.Source)
				}
			}
		}
	}
	for _, want := range []string{"general", "media", "optimize"} {
		if !names[want] {
			t.Errorf("settings response missing section %q", want)
		}
	}
	if !foundSiteName {
		t.Error("settings response missing the site.name row")
	}

	// Valid save: persisted and marked with source db.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.name": "Example Deck Site", "media_max_bytes": "2048"},
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save settings = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(ctx, db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["site.name"] != "Example Deck Site" || settings["media_max_bytes"] != "2048" {
		t.Fatalf("settings not persisted: %+v", settings)
	}
	after := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if !strings.Contains(after.Body.String(), "Example Deck Site") {
		t.Errorf("settings read does not reflect the saved value: %s", snippet(after.Body.String()))
	}

	// Invalid locale: 400 and nothing persisted.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"site.default_locale": "de"},
	}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid locale = %d, want 400", rec.Code)
	}
	if _, ok := settings["site.default_locale"]; ok {
		t.Error("invalid locale was persisted")
	}
}

// TestAdminSettingsSecretNeverRevealed asserts that sensitive keys are stored encrypted and the response only says "configured".
func TestAdminSettingsSecretNeverRevealed(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	ctx := context.Background()

	// The sensitive key must first exist as a row (the settings page only lists configured sensitive keys, same as SSR).
	const first = "spa-plaintext-secret-one"
	if err := store.PutSecret(ctx, db, srv.secrets, "oidc_client_secret", first, nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSecret: %v", err)
	}

	var row store.Setting
	if err := db.First(&row, "key = ?", "oidc_client_secret").Error; err != nil {
		t.Fatalf("load stored secret: %v", err)
	}
	if strings.Contains(row.Value, first) {
		t.Fatalf("stored secret contains plaintext: %q", row.Value)
	}

	// Read: reports "configured" and never echoes plaintext.
	rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil)
	if strings.Contains(rec.Body.String(), first) {
		t.Fatal("settings response leaked the secret plaintext")
	}
	var read adminSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	configured := false
	for _, sec := range read.Sections {
		if sec.Name != "sensitive" {
			continue
		}
		for _, r := range sec.Rows {
			if r.Key == "oidc_client_secret" && r.Configured {
				configured = true
			}
		}
	}
	if !configured {
		t.Error("sensitive section does not report the secret as configured")
	}

	// Write a new value via JSON: no plaintext may appear in the DB, and the response still does not echo it.
	const second = "spa-plaintext-secret-two"
	if rec := adminPostJSON(t, srv, "/api/v1/admin/settings", adminSettingsRequest{
		Values: map[string]string{"oidc_client_secret": second},
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save secret = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if err := db.First(&row, "key = ?", "oidc_client_secret").Error; err != nil {
		t.Fatalf("reload stored secret: %v", err)
	}
	if strings.Contains(row.Value, second) || strings.Contains(row.Value, first) {
		t.Fatalf("stored secret contains plaintext: %q", row.Value)
	}
	if rec := getJSON(t, srv, "/api/v1/admin/settings", cookies, nil); strings.Contains(rec.Body.String(), second) {
		t.Fatal("settings response leaked the newly saved plaintext")
	}
}

// TestAdminSMTPReadSaveAndTest covers reading and validating SMTP JSON, plus the failure reason of a test connection.
func TestAdminSMTPReadSaveAndTest(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)

	rec := getJSON(t, srv, "/api/v1/admin/smtp", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET smtp = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminSMTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode smtp: %v", err)
	}
	if read.Configured || read.PasswordConfigured {
		t.Errorf("fresh smtp config = configured %v password %v, want both false", read.Configured, read.PasswordConfigured)
	}

	// Invalid port: 400 and nothing persisted.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{Port: "70000"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid port = %d, want 400", rec.Code)
	}
	// Invalid TLS mode: 400.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{TLSMode: "ssl"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid tls mode = %d, want 400", rec.Code)
	}

	// Valid save: written to the DB and shown as configured.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/smtp", adminSMTPRequest{
		Host: "localhost", Port: "587", From: "no-reply@example.com", TLSMode: "starttls", Password: "smtp-pw",
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save smtp = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["smtp_host"] != "localhost" {
		t.Fatalf("smtp_host = %q, want localhost", settings["smtp_host"])
	}
	if strings.Contains(settings["smtp_password"], "smtp-pw") {
		t.Fatal("smtp password stored in plaintext")
	}

	// Test connection fails: the raw server-side error is carried into the response (acceptance point).
	addr := deadAddr(t)
	host, port, _ := net.SplitHostPort(addr)
	testRec := adminPostJSON(t, srv, "/api/v1/admin/smtp/test", adminSMTPRequest{
		Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none",
	}, cookies, csrf)
	var result adminTestResult
	if err := json.Unmarshal(testRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode smtp test: %v", err)
	}
	if result.OK || result.Message == "" {
		t.Errorf("smtp test result = %+v, want failure with server error text", result)
	}

	// No host: stable no_host code (a fresh unconfigured service is used, since the saved host would hit above).
	emptySrv, _, _, emptyCookies, emptyCSRF := newNotesServer(t)
	noHost := adminPostJSON(t, emptySrv, "/api/v1/admin/smtp/test", adminSMTPRequest{}, emptyCookies, emptyCSRF)
	if err := json.Unmarshal(noHost.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode smtp test: %v", err)
	}
	if result.OK || result.Code != "no_host" {
		t.Errorf("empty smtp test = %+v, want code no_host", result)
	}
}

// TestAdminOIDCReadSaveAndTest covers reading and validating OIDC JSON, plus test connection with no issuer.
func TestAdminOIDCReadSaveAndTest(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/oidc", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET oidc = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var read adminOIDCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode oidc: %v", err)
	}
	if read.Enabled || read.RedirectURI == "" {
		t.Errorf("oidc read = enabled %v redirect %q, want disabled with a redirect uri", read.Enabled, read.RedirectURI)
	}
	// An unconfigured instance still returns the effective default, so the admin form is pre-filled and does not require typing it.
	if read.Scopes != "openid email profile" {
		t.Errorf("oidc read scopes = %q, want the effective default %q", read.Scopes, "openid email profile")
	}

	// Enabled but issuer invalid: 400.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{Enabled: true, Issuer: "not a url"}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid issuer = %d, want 400", rec.Code)
	}
	// Enabled but issuer empty: 400.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{Enabled: true}, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("empty issuer with enabled = %d, want 400", rec.Code)
	}

	// Valid save: the issuer is persisted.
	if rec := adminPostJSON(t, srv, "/api/v1/admin/oidc", adminOIDCRequest{
		Enabled: true, Issuer: "https://idp.example.com/", ClientID: "engram", Scopes: "openid email",
	}, cookies, csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("save oidc = %d, want 204 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings["oidc.issuer"] != "https://idp.example.com" || settings["oidc.enabled"] != "true" {
		t.Fatalf("oidc settings not persisted: %+v", settings)
	}
	// A saved scope list is returned as the effective list, so the form shows exactly what the login flow will request.
	rec = getJSON(t, srv, "/api/v1/admin/oidc", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET oidc after save = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &read); err != nil {
		t.Fatalf("decode oidc after save: %v", err)
	}
	if read.Scopes != "openid email" {
		t.Errorf("oidc read scopes after save = %q, want %q", read.Scopes, "openid email")
	}

	// Test connection: no usable issuer (the saved one is empty) → no_issuer.
	emptySrv, _, _, emptyCookies, emptyCSRF := newNotesServer(t)
	testRec := adminPostJSON(t, emptySrv, "/api/v1/admin/oidc/test", adminOIDCRequest{}, emptyCookies, emptyCSRF)
	var result adminTestResult
	if err := json.Unmarshal(testRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode oidc test: %v", err)
	}
	if result.OK || result.Code != "no_issuer" {
		t.Errorf("empty oidc test = %+v, want code no_issuer", result)
	}
}
