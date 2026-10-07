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

// 本文件是管理面板「邮件模板」（DESIGN.md §4.7）的验收：切流、读取、保存校验、
// 删除回退、预览的零副作用，以及缺 CSRF 的负例。

// TestSPAAdminMailTemplatePagesCutover 断言页面走 SPA 外壳，且非管理员拿 403。
func TestSPAAdminMailTemplatePagesCutover(t *testing.T) {
	srv, db, _, cookies, _ := newNotesServer(t)
	assertServesSPAShell(t, getWithCookies(t, srv, "/admin/mail-templates", cookies), "GET /admin/mail-templates")

	_, strangerCookies, _ := createUserAndLogin(t, srv, db, "tpl_stranger")
	if rec := getWithCookies(t, srv, "/admin/mail-templates", strangerCookies); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET = %d, want 403", rec.Code)
	}
}

// TestSPAAdminMailTemplatesShipTheBuiltinDefaults 断言列表响应带回每个类型 × 每种语言的
// **内置正文**，且它就是编辑框预填的那一份（DESIGN.md §4.7）。
//
// 这条链之所以要端到端钉住：管理页显示的「默认」如果和发信方兜底不是同一份文本，管理员会
// 以为自己改的是默认，实际改的是另一个东西——而页面上看不出任何异常。
func TestSPAAdminMailTemplatesShipTheBuiltinDefaults(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)

	rec := getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	list := decodeMailTemplates(t, rec.Body.Bytes())
	if len(list.Defaults) == 0 {
		t.Fatal("defaults is empty, want one entry per type and locale")
	}
	byKey := map[string]spaMailTemplateDefault{}
	for _, d := range list.Defaults {
		byKey[d.Type+"|"+d.Locale] = d
	}
	// 每个类型 × 每种语言都要有一份，且能原样通过保存校验（管理员最常见的动作是「不改就存」）。
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
			// 预填的是模板写法（占位符），不是已渲染的邮件——否则管理员编辑的是一封具体的信。
			if !strings.Contains(d.BodyMD, "{{") {
				t.Errorf("%s/%s: default body has no placeholder:\n%s", item.Type, code, d.BodyMD)
			}
		}
	}
	// 两种语言必须是两份不同的文本（否则等于没做本地化）。
	zh, okZH := byKey["password_reset|zh-CN"]
	en, okEN := byKey["password_reset|en"]
	if okZH && okEN && zh.BodyMD == en.BodyMD {
		t.Errorf("zh-CN and en defaults are identical:\n%s", zh.BodyMD)
	}
	// 自定义一份之后默认必须仍在响应里，且与保存前逐字节相同——「恢复默认」按钮靠它回到原件。
	body := `{"subject":"自定义主题","body_md":"点这里重置：{{url}}"}`
	if rec := jsonRequest(t, srv, http.MethodPut,
		"/api/v1/admin/mail-templates/"+string(mail.TypePasswordReset)+"/zh-CN", body, cookies, csrf); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after := decodeMailTemplates(t, getJSON(t, srv, "/api/v1/admin/mail-templates", cookies, nil).Body.Bytes())
	var again spaMailTemplateDefault
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

// decodeMailTemplates 解析列表响应。
func decodeMailTemplates(t *testing.T, body []byte) spaMailTemplatesResponse {
	t.Helper()
	var out spaMailTemplatesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode mail templates: %v", err)
	}
	return out
}

// TestSPAAdminMailTemplatesReadSaveDelete 覆盖读取、校验、落库、审计与删除回退。
func TestSPAAdminMailTemplatesReadSaveDelete(t *testing.T) {
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

	// 必填变量缺失：400 且**不写库**。
	bad := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en",
		`{"subject":"hi","body_md":"no link here"}`, cookies, csrf)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("PUT without the link = %d, want 400 (body %s)", bad.Code, snippet(bad.Body.String()))
	}
	// 引用未知变量同样拒绝。
	unknown := jsonRequest(t, srv, http.MethodPut, "/api/v1/admin/mail-templates/password_reset/en",
		`{"subject":"hi","body_md":"{{url}} {{nope}}"}`, cookies, csrf)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("PUT with an unknown variable = %d, want 400", unknown.Code)
	}
	// 未知类型与不支持的语言分别拒绝。
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

	// 合法保存：200、落库、写审计。
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

	// 删除：回到内置正文，并再写一条审计。
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

// TestSPAAdminMailTemplatePreviewRendersAndWritesNothing 断言预览渲染样例并零副作用。
func TestSPAAdminMailTemplatePreviewRendersAndWritesNothing(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := jsonRequest(t, srv, http.MethodPost, "/api/v1/admin/mail-templates/preview",
		`{"type":"password_reset","locale":"en","subject":"Reset for {{site}}","body_md":"**Hi** {{url}}"}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var out spaMailTemplatePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if out.Subject != "Reset for Engram" {
		t.Errorf("subject = %q, want the sample site substituted", out.Subject)
	}
	if !strings.Contains(out.HTML, "<strong>Hi</strong>") {
		t.Errorf("html = %q, want the template rendered", out.HTML)
	}
	if len(out.Variables) == 0 {
		t.Error("variables is empty, want the sample values shown to the admin")
	}
	if rows, err := store.NewMailTemplateStore(db).List(context.Background()); err != nil || len(rows) != 0 {
		t.Errorf("preview wrote %d rows (err %v), want none", len(rows), err)
	}
}

// TestSPAAdminMailTemplatesRequireCSRF 是必测负例：缺 CSRF 的写路径 403 且零副作用。
// 新增写路径必须同时进这张表，否则这条路径不在任何负例覆盖之内。
func TestSPAAdminMailTemplatesRequireCSRF(t *testing.T) {
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
	// 匿名请求也拿不到列表。
	if rec := getJSON(t, srv, "/api/v1/admin/mail-templates", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", rec.Code)
	}
}
