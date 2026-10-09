package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

func patchProfile(t *testing.T, srv *Server, path string, body any, cookies []*http.Cookie, csrf string, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestProfilePersistence(t *testing.T) {
	srv, db, userID, cookies, csrf := newNotesServer(t)
	initial := getWithCookies(t, srv, "/api/v1/profile", cookies)
	if initial.Code != http.StatusOK {
		t.Fatalf("GET profile = %d, want 200: %s", initial.Code, initial.Body.String())
	}
	var before profilePayload
	if err := json.Unmarshal(initial.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	owner, err := store.NewUserStore(db).ByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if before.ID != owner.PublicID || before.Locale != "zh-CN" {
		t.Fatalf("unexpected initial profile: %+v", before)
	}

	cutoff := 0
	updated := profileRequest{DisplayName: "Updated Name", Locale: "en", Timezone: "Asia/Tokyo", DayCutoffHour: &cutoff}
	patch := patchProfile(t, srv, "/api/v1/profile", updated, cookies, csrf, "")
	if patch.Code != http.StatusOK {
		t.Fatalf("PATCH profile = %d, want 200: %s", patch.Code, patch.Body.String())
	}
	var result profilePayload
	if err := json.Unmarshal(patch.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.DisplayName != updated.DisplayName || result.Locale != updated.Locale || result.Timezone != updated.Timezone || result.DayCutoffHour == nil || *result.DayCutoffHour != 0 {
		t.Fatalf("PATCH response did not reflect saved profile: %+v", result)
	}
	stored, err := store.NewUserStore(db).ByID(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DisplayName != updated.DisplayName || stored.Locale != updated.Locale || stored.Timezone != updated.Timezone || stored.DayCutoffHour == nil || *stored.DayCutoffHour != 0 {
		t.Fatalf("database profile did not persist: %+v", stored)
	}

	readback := getWithCookies(t, srv, "/api/v1/profile", cookies)
	if readback.Code != http.StatusOK || readback.Body.String() != patch.Body.String() {
		t.Fatalf("profile read-back = (%d, %s), want PATCH response", readback.Code, readback.Body.String())
	}

	if rec := patchProfile(t, srv, "/api/v1/profile", updated, cookies, csrf, ""); rec.Code != http.StatusOK {
		t.Fatalf("PATCH unchanged profile = %d: %s", rec.Code, rec.Body.String())
	}
	locale := patchProfile(t, srv, "/api/v1/settings/locale", localeRequest{Locale: "en"}, cookies, csrf, "")
	var localeResponse struct {
		Locale string `json:"locale"`
	}
	if err := json.Unmarshal(locale.Body.Bytes(), &localeResponse); err != nil || locale.Code != http.StatusOK || localeResponse.Locale != "en" {
		t.Fatalf("PATCH unchanged locale = (%d, %s)", locale.Code, locale.Body.String())
	}
	if count, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserProfileUpdate); err != nil || count != 1 {
		t.Fatalf("profile audit rows = (%d, %v), want 1", count, err)
	}

	locale = patchProfile(t, srv, "/api/v1/settings/locale", localeRequest{Locale: "zh-CN"}, cookies, csrf, "")
	if locale.Code != http.StatusOK {
		t.Fatalf("PATCH locale = %d, want 200: %s", locale.Code, locale.Body.String())
	}
	stored, err = store.NewUserStore(db).ByID(context.Background(), userID)
	if err != nil || stored.Locale != "zh-CN" {
		t.Fatalf("locale update persisted as %q, err=%v", stored.Locale, err)
	}
	if count, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserProfileUpdate); err != nil || count != 2 {
		t.Fatalf("profile audit rows after locale change = (%d, %v), want 2", count, err)
	}
}

func TestProfileAuthorizationAndValidation(t *testing.T) {
	srv, db, userID, cookies, csrf := newNotesServer(t)
	valid := profileRequest{DisplayName: "Changed", Locale: "en", Timezone: "UTC"}

	if rec := get(t, srv, "/api/v1/profile", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET profile = %d, want 401", rec.Code)
	}
	if rec := patchProfile(t, srv, "/api/v1/profile", valid, nil, "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous PATCH profile = %d, want 403 CSRF denial", rec.Code)
	}
	if rec := patchProfile(t, srv, "/api/v1/profile", valid, cookies, "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("missing CSRF PATCH profile = %d, want 403", rec.Code)
	}
	if rec := patchProfile(t, srv, "/api/v1/profile", valid, cookies, "invalid", ""); rec.Code != http.StatusForbidden {
		t.Errorf("bad CSRF PATCH profile = %d, want 403", rec.Code)
	}
	if rec := patchProfile(t, srv, "/api/v1/settings/locale", localeRequest{Locale: "en"}, cookies, "", ""); rec.Code != http.StatusForbidden {
		t.Errorf("missing CSRF PATCH locale = %d, want 403", rec.Code)
	}

	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: userID, Name: "profile test", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec := patchProfile(t, srv, "/api/v1/profile", valid, nil, "", "Bearer "+created.Plaintext); rec.Code != http.StatusForbidden {
		t.Errorf("bearer PATCH profile = %d, want 403", rec.Code)
	}
	bearerGet := httptest.NewRequest(http.MethodGet, "/api/v1/profile", nil)
	bearerGet.Header.Set("Authorization", "Bearer "+created.Plaintext)
	bearerRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bearerRec, bearerGet)
	if bearerRec.Code != http.StatusForbidden {
		t.Errorf("bearer GET profile = %d, want 403", bearerRec.Code)
	}

	badCutoff := 24
	cases := []struct {
		name string
		body profileRequest
	}{
		{"empty display name", profileRequest{DisplayName: "  ", Locale: "en", Timezone: "UTC"}},
		{"unsupported locale", profileRequest{DisplayName: "Name", Locale: "fr", Timezone: "UTC"}},
		{"invalid timezone", profileRequest{DisplayName: "Name", Locale: "en", Timezone: "No/Such_Zone"}},
		{"cutoff above range", profileRequest{DisplayName: "Name", Locale: "en", Timezone: "UTC", DayCutoffHour: &badCutoff}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := patchProfile(t, srv, "/api/v1/profile", tc.body, cookies, csrf, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH invalid profile = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if rec := patchProfile(t, srv, "/api/v1/settings/locale", localeRequest{Locale: "fr"}, cookies, csrf, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported locale PATCH = %d, want 400", rec.Code)
	}
	stored, err := store.NewUserStore(db).ByID(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DisplayName != "owner" || stored.Locale != "zh-CN" || stored.Timezone != "Asia/Shanghai" {
		t.Errorf("invalid request modified stored profile: %+v", stored)
	}
	if count, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserProfileUpdate); err != nil || count != 0 {
		t.Errorf("rejected profile requests wrote %d audits, err=%v", count, err)
	}
}

func TestProfileRejectsDisabledSessionUser(t *testing.T) {
	srv, db, userID, cookies, _ := newNotesServer(t)
	if err := store.NewUserStore(db).SetStatus(context.Background(), userID, store.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	rec := getWithCookies(t, srv, "/api/v1/profile", cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session GET profile = %d, want 401", rec.Code)
	}
}
