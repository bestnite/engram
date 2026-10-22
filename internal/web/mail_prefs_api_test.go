package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the end-to-end evidence for the SPA mail notification preference API (internal/web/mail_prefs_api.go):
// it goes through real routes + real SQLite and reuses store.EmailPrefStore and the users row, asserting behaviour identical to the SSR page.
//
// The points covered:
//   - GET groups and switches come strictly from the internal/mail catalog (no more, no less, nothing outside the catalog is exposed);
//   - PATCH writes an explicit choice for every optional type; type A / unknown types are refused and leave no half-saved state;
//   - reminder_hour null and 0 (midnight) are distinguishable, out-of-range values are refused;
//   - writes go through session CSRF, and browser sessions are the only accepted credential.

// notificationPatch submits PATCH JSON with a session cookie and an X-CSRF-Token header.
// Same shape as postJSONWithCSRF, only the method is PATCH (the mail preference API saves with PATCH).
func notificationPatch(t *testing.T, srv *Server, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
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
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// loadNotificationPrefs reads the API response and decodes it; any status other than 200 fails outright.
func loadNotificationPrefs(t *testing.T, rec *httptest.ResponseRecorder) notificationPrefsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var got notificationPrefsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, snippet(rec.Body.String()))
	}
	return got
}

// TestMailPrefsGetMatchesCatalog is the read-side catalog consistency acceptance:
// group order equals ClassOrder, the type set and order equal mail.Catalog, and locked/enabled are derived from CanDisable/ResolveEnabled.
func TestMailPrefsGetMatchesCatalog(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	got := loadNotificationPrefs(t, getWithCookies(t, srv, "/api/v1/settings/notifications", cookies))

	classes := mail.ClassOrder()
	if len(got.Groups) != len(classes) {
		t.Fatalf("group count = %d, want %d (one per class)", len(got.Groups), len(classes))
	}
	catalog := mail.Catalog()
	index := 0
	for i, class := range classes {
		if got.Groups[i].Class != string(class) {
			t.Errorf("group %d class = %q, want %q", i, got.Groups[i].Class, class)
		}
		for _, item := range got.Groups[i].Types {
			if index >= len(catalog) {
				t.Fatalf("response has more types than the catalog: extra %q", item.Type)
			}
			def := catalog[index]
			index++
			if item.Type != string(def.Type) {
				t.Errorf("type %d = %q, want %q (catalog order)", index-1, item.Type, def.Type)
			}
			if want := mail.ResolveEnabled(map[string]bool{}, def.Type); item.Enabled != want {
				t.Errorf("type %q enabled = %v, want %v (directory default)", item.Type, item.Enabled, want)
			}
			if want := !mail.CanDisable(def.Class); item.Locked != want {
				t.Errorf("type %q locked = %v, want %v", item.Type, item.Locked, want)
			}
		}
	}
	if index != len(catalog) {
		t.Fatalf("response covered %d types, want the whole catalog of %d", index, len(catalog))
	}

	if got.ReminderHour != nil {
		t.Errorf("initial reminder_hour = %d, want null (unset)", *got.ReminderHour)
	}
	if got.DefaultReminderHour != reminder.DefaultSendHour {
		t.Errorf("default_reminder_hour = %d, want %d", got.DefaultReminderHour, reminder.DefaultSendHour)
	}
	fresh, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if got.Timezone != fresh.Timezone {
		t.Errorf("timezone = %q, want the user's %q", got.Timezone, fresh.Timezone)
	}
}

// TestMailPrefsPatchRoundTrips accepts "takes effect immediately after saving, and the explicit choice covers every optional type":
// turn on a default-off type C, turn off a default-on type B, write the remaining optional types as off; a fresh read path still agrees.
func TestMailPrefsPatchRoundTrips(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{
			string(mail.TypeReviewReminder): true,  // C: turn on
			string(mail.TypeDeckShared):     false, // B: turn off
		},
		"reminder_hour": 7,
	}, cookies, csrf)
	got := loadNotificationPrefs(t, rec)

	// The response reflects the new state immediately: C on, B off, A still always on.
	if !enabledOf(got, mail.TypeReviewReminder) {
		t.Error("response says review_reminder is off after it was turned on")
	}
	if enabledOf(got, mail.TypeDeckShared) {
		t.Error("response says deck_shared is on after it was turned off")
	}
	if !enabledOf(got, mail.TypePasswordReset) || !lockedOf(got, mail.TypePasswordReset) {
		t.Error("class A password_reset must stay enabled and locked")
	}
	if got.ReminderHour == nil || *got.ReminderHour != 7 {
		t.Fatalf("response reminder_hour = %v, want 7", got.ReminderHour)
	}

	// Storage side: read back with a new instance (equivalent to the read path after a process restart).
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if !choices[string(mail.TypeReviewReminder)] {
		t.Errorf("stored choices = %v, want review_reminder=true", choices)
	}
	if choices[string(mail.TypeDeckShared)] {
		t.Errorf("stored choices = %v, want deck_shared=false", choices)
	}
	// Every optional type must have an explicit choice (absent = off), and the count matches the optional types in the catalog.
	wantDisableable := 0
	for _, def := range mail.Catalog() {
		if mail.CanDisable(def.Class) {
			wantDisableable++
		}
	}
	if len(choices) != wantDisableable {
		t.Errorf("stored choice count = %d, want %d (one per disableable type)", len(choices), wantDisableable)
	}
	u, err := store.NewUserStore(db).ByID(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if u.ReminderHour == nil || *u.ReminderHour != 7 {
		t.Fatalf("stored reminder_hour = %v, want 7", u.ReminderHour)
	}

	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserEmailPrefsUpdate); err != nil || n != 1 {
		t.Fatalf("user.email_prefs_update audit = (%d, %v), want 1", n, err)
	}
}

// TestMailPrefsReminderHourNullAndMidnight accepts three key points about the send hour:
// 0 is stored as 0 (midnight, never NULL), null is stored as NULL, and an out-of-range value is refused with 400 without touching the stored value.
func TestMailPrefsReminderHourNullAndMidnight(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	users := store.NewUserStore(db)
	load := func() *store.User {
		t.Helper()
		u, err := users.ByID(context.Background(), ownerID)
		if err != nil {
			t.Fatalf("load owner: %v", err)
		}
		return u
	}

	// 0 o'clock: must be stored as 0, and must not be conflated with "not set".
	if rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": 0}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("saving hour 0 status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour == nil {
		t.Fatal("explicit 0 was stored as NULL: midnight and unset are conflated")
	} else if *u.ReminderHour != 0 {
		t.Fatalf("stored reminder_hour = %d, want 0", *u.ReminderHour)
	}

	// null: back to the site default (NULL).
	if rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": nil}, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("saving the site default status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour != nil {
		t.Fatalf("after choosing the site default reminder_hour = %d, want NULL", *u.ReminderHour)
	}

	// Out of range: 400, and the stored value is untouched (still NULL).
	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{"reminder_hour": 24}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("saving hour 24 status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "reminder_hour_invalid") {
		t.Errorf("rejection does not carry the reminder_hour_invalid code: %s", snippet(rec.Body.String()))
	}
	if u := load(); u.ReminderHour != nil {
		t.Fatalf("a rejected reminder_hour wrote %d, want no change (NULL)", *u.ReminderHour)
	}
}

// TestMailPrefsRejectsClassA is the negative case for "refusing to turn type A off": a submission targeting A gets 400 and writes no rows.
func TestMailPrefsRejectsClassA(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypePasswordReset): true},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disabling a class A type status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "class_locked") {
		t.Errorf("rejection does not carry the class_locked code: %s", snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a rejected class A submission wrote choices: %v", choices)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserEmailPrefsUpdate); err != nil || n != 0 {
		t.Fatalf("audit after a rejected submission = (%d, %v), want 0", n, err)
	}
}

// TestMailPrefsRejectsUnknownType asserts types outside the catalog are refused: not exposed, not accepted, not written.
func TestMailPrefsRejectsUnknownType(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)

	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{"totally_made_up": true},
	}, cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown type status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "unknown_type") {
		t.Errorf("rejection does not carry the unknown_type code: %s", snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a rejected unknown-type submission wrote choices: %v", choices)
	}
}

// TestMailPrefsRejectsMissingCSRF asserts that saving must carry the session-bound CSRF token; missing means refused with nothing written.
func TestMailPrefsRejectsMissingCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)

	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypeInvite): true},
	}, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH without csrf status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	if len(choices) != 0 {
		t.Errorf("a CSRF-less request wrote choices: %v", choices)
	}
}

// TestMailPrefsRejectsAnonymousAndBearer asserts the API accepts browser sessions only; neither reads nor writes accept bearer credentials.
func TestMailPrefsRejectsAnonymousAndBearer(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)

	if rec := getWithCookies(t, srv, "/api/v1/settings/notifications", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}
	if rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{}, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous PATCH = %d, want 403 CSRF denial", rec.Code)
	}

	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: ownerID, Name: "spa mail prefs test", Scopes: []string{store.ScopeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest(http.MethodGet, "/api/v1/settings/notifications", nil)
	bearer.Header.Set("Authorization", "Bearer "+created.Plaintext)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, bearer)
	if rec.Code != http.StatusForbidden {
		t.Errorf("bearer GET = %d, want 403", rec.Code)
	}
}

// TestMailPrefsAdminClassIsAdminOnly is the negative case for "a non-admin can neither see nor set type D":
// the response has only the A/B/C groups and no type D at all; submitting type D is refused with 403 + forbidden_type and writes nothing;
// and a normal save writes no explicit choice for type D on a non-admin either (falling back to the catalog default = on if promoted later).
func TestMailPrefsAdminClassIsAdminOnly(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	member, err := srv.accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "member", Email: "member@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	cookies, csrf := loginJSON(t, srv, db, "member", "Sup3rSecret!")

	got := loadNotificationPrefs(t, getWithCookies(t, srv, "/api/v1/settings/notifications", cookies))
	wantGroups := 0
	for _, class := range mail.ClassOrder() {
		if !mail.AdminOnly(class) {
			wantGroups++
		}
	}
	if len(got.Groups) != wantGroups {
		t.Fatalf("non-admin group count = %d, want %d (D 类不展示)", len(got.Groups), wantGroups)
	}
	for _, group := range got.Groups {
		if mail.AdminOnly(mail.Class(group.Class)) {
			t.Errorf("non-admin response contains the %q group, want none", group.Class)
		}
	}
	for _, def := range mail.Catalog() {
		if mail.AdminOnly(def.Class) && hasNotificationType(got, def.Type) {
			t.Errorf("non-admin response exposes the admin-only type %q", def.Type)
		}
	}

	// Submit type D: 403 + forbidden_type, and not a single row is written.
	rec := notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypeJobFailed): false},
	}, cookies, csrf)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin D-class submission status = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "forbidden_type") {
		t.Errorf("rejection does not carry the forbidden_type code: %s", snippet(rec.Body.String()))
	}
	if choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), member.ID); err != nil {
		t.Fatalf("Choices() error = %v", err)
	} else if len(choices) != 0 {
		t.Errorf("a rejected D-class submission wrote choices: %v", choices)
	}

	// A normal save (without the type D keys) succeeds, and no explicit choice for type D appears in the database.
	rec = notificationPatch(t, srv, "/api/v1/settings/notifications", map[string]any{
		"choices": map[string]bool{string(mail.TypeDeckShared): true}, "reminder_hour": 8,
	}, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("non-admin save status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	choices, err := store.NewEmailPrefStore(db).Choices(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("Choices() error = %v", err)
	}
	wantChoices := 0
	for _, def := range mail.Catalog() {
		if !mail.CanDisable(def.Class) || mail.AdminOnly(def.Class) {
			continue
		}
		wantChoices++
		if _, ok := choices[string(def.Type)]; !ok {
			t.Errorf("non-admin save did not write an explicit choice for %q", def.Type)
		}
	}
	if len(choices) != wantChoices {
		t.Errorf("stored choice count = %d, want %d (可关闭且非 D 类的类型)", len(choices), wantChoices)
	}
	for _, def := range mail.Catalog() {
		if !mail.AdminOnly(def.Class) {
			continue
		}
		if _, ok := choices[string(def.Type)]; ok {
			t.Errorf("non-admin save wrote the admin-only choice %q", def.Type)
		}
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionUserEmailPrefsUpdate); err != nil || n != 1 {
		t.Fatalf("user.email_prefs_update audit = (%d, %v), want 1 (only the accepted save)", n, err)
	}
	if after := loadNotificationPrefs(t, getWithCookies(t, srv, "/api/v1/settings/notifications", cookies)); len(after.Groups) != wantGroups {
		t.Errorf("after save non-admin group count = %d, want %d", len(after.Groups), wantGroups)
	}
}

// hasNotificationType reports whether a type appears in the response.
// enabledOf/lockedOf also return false when the type is missing, so they cannot tell "absent" from "value is false"; that is why this helper exists.
func hasNotificationType(resp notificationPrefsResponse, typ mail.Type) bool {
	for _, group := range resp.Groups {
		for _, item := range group.Types {
			if item.Type == string(typ) {
				return true
			}
		}
	}
	return false
}

// enabledOf reports a type's effective switch in the response; a missing type fails outright.
func enabledOf(resp notificationPrefsResponse, typ mail.Type) bool {
	for _, group := range resp.Groups {
		for _, item := range group.Types {
			if item.Type == string(typ) {
				return item.Enabled
			}
		}
	}
	return false
}

// lockedOf reports whether a type is locked in the response; a missing type fails outright.
func lockedOf(resp notificationPrefsResponse, typ mail.Type) bool {
	for _, group := range resp.Groups {
		for _, item := range group.Types {
			if item.Type == string(typ) {
				return item.Locked
			}
		}
	}
	return false
}
