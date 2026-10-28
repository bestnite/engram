package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance test for the admin panel's "mail templates": cutover, reads, save validation,
// delete fallback, preview's side-effect freeness, and the negative case of a missing CSRF.

// TestAdminMailTemplatePagesCutover asserts the pages go through the SPA shell and non-admins get 403.
func TestAdminMailTemplatePagesCutover(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	assertShell(t, getWithCookies(t, srv, "/admin/mail-templates", cookies))

	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "tpl_stranger")
	if rec := getWithCookies(t, srv, "/admin/mail-templates", strangerCookies); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET = %d, want 403", rec.Code)
	}
}

// TestAdminMailTemplatesShipTheBuiltinDefaults asserts the list response carries the
// **built-in body** for each type × each language, and that it is exactly what the editor is pre-filled with.
//
// This chain is pinned end to end because: if the "default" shown on the admin page is not the same text as the sender's fallback, the admin will
// think they are editing the default while actually editing something else — and nothing on the page looks wrong.
func TestAdminMailTemplatesShipTheBuiltinDefaults(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// No template is saved yet: rows must be an empty array, not null.
	if body := rec.Body.String(); !strings.Contains(body, `"rows":[]`) {
		t.Errorf("GET body has no empty rows array: %s", snippet(body))
	}
	list := decodeMailTemplates(t, rec.Body.Bytes())
	if len(list.Defaults) == 0 {
		t.Fatal("defaults is empty, want one entry per type and locale")
	}
	byKey := map[string]mailTemplateDefault{}
	for _, d := range list.Defaults {
		byKey[d.Type+"|"+d.Locale] = d
	}
	// Each type × each language must have one, and it must pass save validation as-is (the most common admin action is "save without changing anything").
	for _, item := range list.Types {
		for _, code := range list.Locales {
			d, ok := byKey[item.Type+"|"+code]
			if !ok {
				t.Errorf("no built-in default for %s/%s", item.Type, code)
				continue
			}
			if strings.TrimSpace(d.Subject) == "" || strings.TrimSpace(d.BodyMD) == "" {
				t.Errorf("%s/%s: default is empty", item.Type, code)
			}
			if err := mail.Validate(mail.Type(item.Type), d.Subject, d.BodyMD); err != nil {
				t.Errorf("%s/%s: the shipped default does not pass Validate: %v", item.Type, code, err)
			}
			// What is pre-filled is the template form (placeholders), not an already-rendered mail — otherwise the admin would be editing one specific letter.
			if !strings.Contains(d.BodyMD, "{{") {
				t.Errorf("%s/%s: default body has no placeholder:\n%s", item.Type, code, d.BodyMD)
			}
		}
	}
	// The two locales must be two different texts (otherwise it amounts to no localization at all).
	zh, okZH := byKey["password_reset|zh-CN"]
	en, okEN := byKey["password_reset|en"]
	if okZH && okEN && zh.BodyMD == en.BodyMD {
		t.Errorf("zh-CN and en defaults are identical:\n%s", zh.BodyMD)
	}
	// After customizing one, the default must still be in the response and byte-identical to before saving — that is what the "restore default" button uses to get back to the original.
	body := `{"subject":"自定义主题","body_md":"点这里重置：{{url}}"}`
	if rec := jsonRequest(t, srv, http.MethodPut,
		"/api/v1/admin/mail-templates/"+string(mail.TypePasswordReset)+"/zh-CN", body, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after := decodeMailTemplates(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil).Body.Bytes())
	var again mailTemplateDefault
	for _, d := range after.Defaults {
		if d.Type == "password_reset" && d.Locale == "zh-CN" {
			again = d
		}
	}
	if again.BodyMD != zh.BodyMD || again.Subject != zh.Subject {
		t.Errorf("default changed after saving a custom template:\n before = %q / %q\n after  = %q / %q",
			zh.Subject, zh.BodyMD, again.Subject, again.BodyMD)
	}
}

// decodeMailTemplates decodes the list response.
func decodeMailTemplates(t *testing.T, body []byte) mailTemplatesResponse {
	t.Helper()
	var out mailTemplatesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode mail templates: %v", err)
	}
	return out
}

// TestAdminMailTemplatesReadSaveDelete covers reads, validation, persistence, audit and delete fallback.
func TestAdminMailTemplatesReadSaveDelete(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	rec := getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	list := decodeMailTemplates(t, rec.Body.Bytes())
	if len(list.Locales) == 0 {
		t.Error("locales is empty, want the supported languages")
	}
	if len(list.Rows) != 0 {
		t.Errorf("rows = %d, want 0 before anything is saved", len(list.Rows))
	}
	foundReset := false
	for _, item := range list.Types {
		if item.Type != string(mail.TypePasswordReset) {
			continue
		}
		foundReset = true
		if len(item.Vars) == 0 {
			t.Error("password_reset has no variables, want the variable list")
		}
		var required int
		for _, v := range item.Vars {
			if v.Required {
				required++
				if v.NoteKey == "" {
					t.Errorf("variable %q has an empty note key", v.Name)
				}
			}
		}
		if required == 0 {
			t.Error("password_reset has no required variables, want at least the link")
		}
	}
	if !foundReset {
		t.Fatal("password_reset missing from the type list")
	}

	// A required variable is missing: 400 and **no DB write**.
	bad := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en",
		`{"subject":"hi","body_md":"no link here"}`, cookies, csrf)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("PUT without the link = %d, want 400 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	// Referencing an unknown variable is rejected as well.
	unknown := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en",
		`{"subject":"hi","body_md":"{{url}} {{nope}}"}`, cookies, csrf)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("PUT with an unknown variable = %d, want 400", unknown.Code)
	}
	// An unknown type and an unsupported locale are each rejected.
	if rec := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/no_such_type/en",
		`{"subject":"s","body_md":"{{url}}"}`, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unknown type = %d, want 400", rec.Code)
	}
	if rec := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/xx-YY",
		`{"subject":"s","body_md":"{{url}}"}`, cookies, csrf); rec.Code != http.StatusBadRequest {
		t.Errorf("PUT unsupported locale = %d, want 400", rec.Code)
	}
	rows, err := store.NewMailTemplateStore(db).List(ctx)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d after rejected saves, want 0", len(rows))
	}

	// Valid save: 200, persisted, audit row written.
	ok := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en",
		`{"subject":"Reset for {{site}}","body_md":"**Hi** {{url}} ({{expires}})"}`, cookies, csrf)
	if ok.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body %s)", ok.Code, snippet(ok.Body.String()))
	}
	list = decodeMailTemplates(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil).Body.Bytes())
	if len(list.Rows) != 1 || list.Rows[0].Type != "password_reset" || list.Rows[0].Locale != "en" {
		t.Fatalf("rows after save = %+v, want one password_reset/en row", list.Rows)
	}
	if list.Rows[0].Subject != "Reset for {{site}}" || list.Rows[0].UpdatedAt == "" {
		t.Errorf("saved row = %+v, want the subject and an update timestamp", list.Rows[0])
	}
	if n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionMailTemplateUpdate); err != nil {
		t.Fatalf("count audit: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows after save = %d, want 1", n)
	}

	// Delete: falls back to the built-in body and writes another audit row.
	del := jsonRequest(t, srv, http.MethodDelete, "/api/v1/admin/mail-templates/password_reset/en", "", cookies, csrf)
	if del.Code != http.StatusOK {
		t.Fatalf("DELETE = %d, want 200 (body %s)", del.Code, snippet(del.Body.String()))
	}
	list = decodeMailTemplates(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil).Body.Bytes())
	if len(list.Rows) != 0 {
		t.Errorf("rows after delete = %d, want 0", len(list.Rows))
	}
	if n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionMailTemplateUpdate); err != nil {
		t.Fatalf("count audit: %v", err)
	} else if n != 2 {
		t.Errorf("audit rows after delete = %d, want 2", n)
	}
}

// TestAdminMailTemplatePreviewRendersAndWritesNothing asserts preview renders the samples and has zero side effects.
func TestAdminMailTemplatePreviewRendersAndWritesNothing(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/admin/mail-templates/preview",
		`{"type":"password_reset","locale":"en","subject":"Reset for {{site}}","body_md":"**Hi** {{url}}"}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var out mailTemplatePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if out.Subject != "Reset for Engram" {
		t.Errorf("subject = %q, want the sample site substituted", out.Subject)
	}
	if strings.TrimSpace(out.HTML) == "" {
		t.Error("html is empty, want a rendered body")
	}
	if !strings.Contains(out.HTML, "Hi</strong>") {
		t.Errorf("html = %q, want the template rendered", out.HTML)
	}
	if len(out.Variables) == 0 {
		t.Error("variables is empty, want the sample values shown to the admin")
	}
	if rows, err := store.NewMailTemplateStore(db).List(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("preview wrote %d rows (err %v), want none", len(rows), err)
	}
}

// TestAdminMailTemplatePreviewMatchesBuiltinDefaultSend pins the identity the editor depends on: previewing the built-in
// default produces exactly the HTML that sending it after an unchanged save produces, for the same type, variables and locale.
// If preview and send diverged, the admin would approve one mail and send another.
func TestAdminMailTemplatePreviewMatchesBuiltinDefaultSend(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	list := decodeMailTemplates(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil).Body.Bytes())
	var def mailTemplateDefault
	for _, d := range list.Defaults {
		if d.Type == string(mail.TypePasswordReset) && d.Locale == "en" {
			def = d
		}
	}
	if def.BodyMD == "" {
		t.Fatal("no password_reset/en built-in default")
	}

	// "Save without changing anything" is the most common admin action; the row must render identically to the built-in body.
	saveBody, err := json.Marshal(mailTemplateSaveRequest{Subject: def.Subject, BodyMD: def.BodyMD})
	if err != nil {
		t.Fatalf("marshal save request: %v", err)
	}
	if rec := jsonRequest(t, srv, http.MethodPut,
		"/api/v1/admin/mail-templates/"+string(mail.TypePasswordReset)+"/en", string(saveBody), cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	previewBody, err := json.Marshal(mailTemplatePreviewRequest{
		Type: string(mail.TypePasswordReset), Locale: "en", Subject: def.Subject, BodyMD: def.BodyMD,
	})
	if err != nil {
		t.Fatalf("marshal preview request: %v", err)
	}
	rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/admin/mail-templates/preview", string(previewBody), cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var preview mailTemplatePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}

	// Sending the saved template with the same variables and locale goes through the real sender entry point.
	vars := sampleVars(mail.TypePasswordReset)
	loc := srv.i18n.Localizer(srv.i18n.Pick("en", "", ""))
	fallbackSubject, fallbackBodyMD, _ := mail.DefaultTemplate(mail.TypePasswordReset, mail.VariantDefault, loc.Tf, vars)
	subject, text, html := srv.renderMail(context.Background(), loc, mail.TypePasswordReset, vars, fallbackSubject, fallbackBodyMD, "")

	if preview.Subject != subject {
		t.Errorf("preview subject = %q, sent subject = %q", preview.Subject, subject)
	}
	if preview.Text != text {
		t.Errorf("preview text differs from the sent plain-text part:\n preview = %q\n sent    = %q", preview.Text, text)
	}
	if preview.HTML != html {
		t.Errorf("preview HTML differs from the sent HTML:\n preview = %q\n sent    = %q", preview.HTML, html)
	}
}

// TestAdminMailTemplatesRequireCSRF is a mandatory negative case: a write path without CSRF is 403 with zero side effects.
// A newly added write path must also go into this table, otherwise that path is not covered by any negative case.
func TestAdminMailTemplatesRequireCSRF(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	for _, target := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en", `{"subject":"s","body_md":"{{url}} {{expires}}"}`},
		{http.MethodDelete, "/api/v1/admin/mail-templates/password_reset/en", ""},
		{http.MethodPost, "/api/v1/admin/mail-templates/preview", `{"type":"password_reset","body_md":"{{url}}"}`},
		{http.MethodPost, "/api/v1/admin/mail-templates/test", `{"type":"password_reset","body_md":"{{url}}"}`},
	} {
		rec := jsonRequest(t, srv, target.method, target.path, target.body, cookies, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without CSRF = %d, want 403 (body %s)", target.method, target.path, rec.Code, snippet(rec.Body.String()))
		}
	}
	if rows, err := store.NewMailTemplateStore(db).List(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("CSRF-less writes left %d rows (err %v), want 0", len(rows), err)
	}
	// Anonymous requests do not get the list either.
	if rec := getJSON(t, srv, "/api/v1/admin/mail-templates", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}
}
